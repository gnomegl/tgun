package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

type SessionData struct {
	Config struct {
		AppID   int    `json:"app_id"`
		AppHash string `json:"app_hash"`
	} `json:"config"`
	Data session.Data `json:"data"`
}

func getSessionPath() string {
	homeDir, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		return filepath.Join(homeDir, "AppData", "Local", "tgun", "session.json")
	}
	return filepath.Join(homeDir, ".config", "tgun", "session.json")
}

func loadSession(sessionFile string) (*SessionData, error) {
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		return nil, err
	}

	var sessionData SessionData
	if err := json.Unmarshal(data, &sessionData); err != nil {
		return nil, err
	}

	return &sessionData, nil
}

type Result struct {
	Username  string
	Available bool
	Error     error
}

func checkSingleUsername(ctx context.Context, client *telegram.Client, username string) (bool, error) {
	username = strings.TrimPrefix(username, "@")

	if username == "" {
		return false, fmt.Errorf("empty username")
	}

	result, err := client.API().AccountCheckUsername(ctx, username)
	if err != nil {
		return false, err
	}

	return result, nil
}

func checkUsernames(usernames []string, sessionFile string) ([]Result, error) {
	sessionData, err := loadSession(sessionFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	apiID := 1
	apiHash := "placeholder"
	if sessionData.Config.AppID != 0 {
		apiID = sessionData.Config.AppID
	}
	if sessionData.Config.AppHash != "" {
		apiHash = sessionData.Config.AppHash
	}

	sessionStorage := &session.FileStorage{Path: sessionFile}

	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: sessionStorage,
	})

	var results []Result

	err = client.Run(context.Background(), func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("failed to get auth status: %w", err)
		}

		if !status.Authorized {
			return fmt.Errorf("not authorized, please log in first using 'tgun login'.\nIf you've already logged in, your session may have expired or been invalidated")
		}

		rateLimiter := time.Tick(200 * time.Millisecond)

		var wg sync.WaitGroup
		resultChan := make(chan Result, len(usernames))

		var processed int
		var mu sync.Mutex
		totalCount := len(usernames)

		progressTicker := time.NewTicker(1 * time.Second)
		defer progressTicker.Stop()

		go func() {
			for range progressTicker.C {
				mu.Lock()
				current := processed
				mu.Unlock()

				if current < totalCount {
					percentage := float64(current) / float64(totalCount) * 100
					fmt.Printf("\rProgress: %d/%d (%.1f%%) complete",
						current, totalCount, percentage)
				}
			}
		}()

		for _, username := range usernames {
			wg.Add(1)
			go func(u string) {
				defer wg.Done()
				<-rateLimiter

				available, err := checkSingleUsername(ctx, client, u)
				result := Result{
					Username:  u,
					Available: available,
					Error:     err,
				}

				resultChan <- result

				mu.Lock()
				processed++
				mu.Unlock()
			}(username)
		}

		go func() {
			wg.Wait()
			close(resultChan)
			fmt.Print("\r\r")
		}()

		for result := range resultChan {
			results = append(results, result)
		}

		return nil
	})

	return results, err
}

func loadUsernamesFromFile(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var usernames []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		username := strings.TrimSpace(scanner.Text())
		if username != "" && !strings.HasPrefix(username, "#") {
			usernames = append(usernames, username)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return usernames, nil
}

func printHelp() {
	fmt.Println("tgun - Telegram Username Checker")
	fmt.Println("\nUsage:")
	fmt.Println("  tgun login [session_file]             Login to Telegram API")
	fmt.Println("  tgun <username> [session_file]        Check a single username")
	fmt.Println("  tgun -f, --file <file> [session_file] Check usernames from a file (one per line)")

	fmt.Println("  tgun -h, --help                       Show this help message")
	fmt.Println("\nOptions:")
	fmt.Println("  login                     Login to Telegram API")
	fmt.Println("  <username>                Username to check (without @)")
	fmt.Println("  -f, --file <file>         File containing usernames to check (one per line)")

	fmt.Println("  -h, --help                Show this help message")
	fmt.Println("  [session_file]            Optional path to session file (default: ~/.config/tgun/session.json)")
	fmt.Println("\nExamples:")
	fmt.Println("  tgun login")
	fmt.Println("  tgun username123")
	fmt.Println("  tgun -f usernames.txt")
	fmt.Println("  tgun -f wordlist.txt ~/custom_session.json")
	fmt.Println("\nNotes:")
	fmt.Println("  - You must login first using 'tgun login' before checking usernames")
	fmt.Println("  - If you have 2FA enabled, you will be prompted for your password during login")
	fmt.Println("  - Available usernames will be saved to 'available_usernames.txt'")
	fmt.Println("  - Comments in files (lines starting with #) are ignored")
	fmt.Println("  - Any text file can be used as input (wordlists, username lists, etc.)")
	fmt.Println("  - Real-time progress updates are shown for batch operations")
}

type customAuth struct {
	phone string
}

func (a *customAuth) Phone(ctx context.Context) (string, error) {
	return a.phone, nil
}

func (a *customAuth) Password(ctx context.Context) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("\n2FA password required!")
	fmt.Print("Enter your 2FA password: ")
	password, _ := reader.ReadString('\n')
	return strings.TrimSpace(password), nil
}

func (a *customAuth) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter the code you received: ")
	code, _ := reader.ReadString('\n')
	return strings.TrimSpace(code), nil
}

func (a *customAuth) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	fmt.Println("Automatically accepting Terms of Service")
	return nil
}

func (a *customAuth) SignUp(ctx context.Context) (auth.UserInfo, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Enter your first name: ")
	firstName, _ := reader.ReadString('\n')
	firstName = strings.TrimSpace(firstName)

	fmt.Print("Enter your last name (optional): ")
	lastName, _ := reader.ReadString('\n')
	lastName = strings.TrimSpace(lastName)

	return auth.UserInfo{
		FirstName: firstName,
		LastName:  lastName,
	}, nil
}

func login(sessionFile string) error {
	sessionDir := filepath.Dir(sessionFile)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Enter your Telegram API ID (from https://my.telegram.org): ")
	apiIDStr, _ := reader.ReadString('\n')
	apiIDStr = strings.TrimSpace(apiIDStr)
	apiID, err := strconv.Atoi(apiIDStr)
	if err != nil {
		return fmt.Errorf("invalid API ID: %w", err)
	}

	fmt.Print("Enter your Telegram API Hash: ")
	apiHash, _ := reader.ReadString('\n')
	apiHash = strings.TrimSpace(apiHash)

	fmt.Print("Enter your phone number (with country code, e.g. +1234567890): ")
	phone, _ := reader.ReadString('\n')
	phone = strings.TrimSpace(phone)

	sessionStorage := &session.FileStorage{Path: sessionFile}

	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: sessionStorage,
	})

	sessionData := &SessionData{}
	sessionData.Config.AppID = apiID
	sessionData.Config.AppHash = apiHash

	data, err := json.MarshalIndent(sessionData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}

	if err := os.WriteFile(sessionFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	userAuth := &customAuth{
		phone: phone,
	}

	fmt.Println("\nStarting authentication process...")
	fmt.Println("Note: If you have 2FA enabled, you will be prompted for your password.")
	fmt.Println("If this is a new account, you will be asked to provide your name.")

	return client.Run(context.Background(), func(ctx context.Context) error {
		flow := auth.NewFlow(userAuth, auth.SendCodeOptions{})

		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			if strings.Contains(err.Error(), "2FA") || strings.Contains(err.Error(), "PASSWORD_HASH_INVALID") {
				return fmt.Errorf("authentication failed: 2FA password was incorrect: %w", err)
			}
			return fmt.Errorf("authentication failed: %w", err)
		}

		fmt.Println("\nSuccessfully logged in!")
		return nil
	})
}

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	if os.Args[1] == "-h" || os.Args[1] == "--help" {
		printHelp()
		os.Exit(0)
	}

	sessionFile := getSessionPath()
	var usernames []string

	if os.Args[1] == "login" {
		if len(os.Args) >= 3 {
			sessionFile = os.Args[2]
		}

		fmt.Printf("Logging in to Telegram API (session will be saved to %s)\n", sessionFile)
		if err := login(sessionFile); err != nil {
			fmt.Printf("ERROR: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	} else if os.Args[1] == "-f" || os.Args[1] == "--file" {
		if len(os.Args) < 3 {
			fmt.Println("Error: Missing file path")
			fmt.Println("Usage: tgun -f <file_with_usernames> [session_file]")
			fmt.Println("Use 'tgun --help' for more information")
			os.Exit(1)
		}

		usernamesFile := os.Args[2]

		if len(os.Args) >= 4 {
			sessionFile = os.Args[3]
		}

		fmt.Printf("Loading usernames from file: %s\n", usernamesFile)
		loadedUsernames, err := loadUsernamesFromFile(usernamesFile)
		if err != nil {
			fmt.Printf("ERROR: Failed to load usernames from file: %v\n", err)
			os.Exit(1)
		}

		usernames = loadedUsernames
		fmt.Printf("Loaded %d usernames from file\n", len(usernames))
	} else {
		username := os.Args[1]

		if len(os.Args) >= 3 {
			sessionFile = os.Args[2]
		}

		usernames = []string{username}
	}

	results, err := checkUsernames(usernames, sessionFile)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}

	var available, taken, errors int

	for _, result := range results {
		if result.Error != nil {
			fmt.Printf("[ERROR] %s: %v\n", result.Username, result.Error)
			errors++
		} else if result.Available {
			fmt.Printf("[AVAILABLE] %s\n", result.Username)
			available++
		} else {
			fmt.Printf("[TAKEN] %s\n", result.Username)
			taken++
		}
	}

	fmt.Println("\nSummary:")
	fmt.Printf("Total: %d, Available: %d, Taken: %d, Errors: %d\n",
		len(results), available, taken, errors)

	if available > 0 {
		outputFile := "available_usernames.txt"
		f, err := os.Create(outputFile)
		if err != nil {
			fmt.Printf("ERROR: Failed to create output file: %v\n", err)
		} else {
			defer f.Close()

			for _, result := range results {
				if result.Error == nil && result.Available {
					f.WriteString(result.Username + "\n")
				}
			}

			fmt.Printf("Available usernames written to %s\n", outputFile)
		}
	}
}
