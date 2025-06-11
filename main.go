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

type Result struct {
	Username  string
	Available bool
	Error     error
}

type customAuth struct {
	phone string
}

func (a *customAuth) Phone(ctx context.Context) (string, error) {
	return a.phone, nil
}

func (a *customAuth) Password(ctx context.Context) (string, error) {
	return promptInput("\n2FA password required!\nEnter your 2FA password: "), nil
}

func (a *customAuth) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	return promptInput("Enter the code you received: "), nil
}

func (a *customAuth) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	fmt.Println("Automatically accepting Terms of Service")
	return nil
}

func (a *customAuth) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{
		FirstName: promptInput("Enter your first name: "),
		LastName:  promptInput("Enter your last name (optional): "),
	}, nil
}

func getSessionPath() string {
	homeDir, _ := os.UserHomeDir()
	subPath := ".config"
	if runtime.GOOS == "windows" {
		subPath = filepath.Join("AppData", "Local")
	}
	return filepath.Join(homeDir, subPath, "tgun", "session.json")
}

func loadSession(sessionFile string) (*SessionData, error) {
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		return nil, err
	}
	var sessionData SessionData
	return &sessionData, json.Unmarshal(data, &sessionData)
}

func promptInput(prompt string) string {
	fmt.Print(prompt)
	input, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(input)
}

func checkUsernames(usernames []string, sessionFile string) ([]Result, error) {
	sessionData, err := loadSession(sessionFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	apiID, apiHash := 1, "placeholder"
	if sessionData.Config.AppID != 0 {
		apiID = sessionData.Config.AppID
	}
	if sessionData.Config.AppHash != "" {
		apiHash = sessionData.Config.AppHash
	}

	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: sessionFile},
	})

	var results []Result
	err = client.Run(context.Background(), func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("failed to get auth status: %w", err)
		}
		if !status.Authorized {
			return fmt.Errorf("not authorized, please log in first using 'tgun login'")
		}

		rateLimiter := time.Tick(200 * time.Millisecond)
		var wg sync.WaitGroup
		resultChan := make(chan Result, len(usernames))
		var processed int
		var mu sync.Mutex

		progressTicker := time.NewTicker(1 * time.Second)
		defer progressTicker.Stop()

		go func() {
			for range progressTicker.C {
				mu.Lock()
				current := processed
				mu.Unlock()
				if current < len(usernames) {
					fmt.Printf("\rProgress: %d/%d (%.1f%%) complete", current, len(usernames), float64(current)/float64(len(usernames))*100)
				}
			}
		}()

		for _, username := range usernames {
			wg.Add(1)
			go func(u string) {
				defer wg.Done()
				<-rateLimiter
				u = strings.TrimPrefix(u, "@")
				available, err := client.API().AccountCheckUsername(ctx, u)
				resultChan <- Result{Username: u, Available: available, Error: err}
				mu.Lock()
				processed++
				mu.Unlock()
			}(username)
		}

		go func() {
			wg.Wait()
			close(resultChan)
			fmt.Print("\r\033[K")
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
		if username := strings.TrimSpace(scanner.Text()); username != "" && !strings.HasPrefix(username, "#") {
			usernames = append(usernames, username)
		}
	}
	return usernames, scanner.Err()
}

func printHelp() {
	help := `tgun - Telegram Username Checker

Usage:
  tgun login [session_file]             Login to Telegram API
  tgun <username> [session_file]        Check a single username
  tgun -f, --file <file> [session_file] Check usernames from a file (one per line)
  tgun -h, --help                       Show this help message

Examples:
  tgun login
  tgun username123
  tgun -f usernames.txt
  tgun -f wordlist.txt ~/custom_session.json

Notes:
  - You must login first using 'tgun login' before checking usernames
  - If you have 2FA enabled, you will be prompted for your password during login
  - Available usernames will be saved to 'available_usernames.txt'
  - Comments in files (lines starting with #) are ignored`
	fmt.Println(help)
}

func login(sessionFile string) error {
	if err := os.MkdirAll(filepath.Dir(sessionFile), 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	apiID, err := strconv.Atoi(promptInput("Enter your Telegram API ID (from https://my.telegram.org): "))
	if err != nil {
		return fmt.Errorf("invalid API ID: %w", err)
	}

	apiHash := promptInput("Enter your Telegram API Hash: ")
	phone := promptInput("Enter your phone number (with country code, e.g. +1234567890): ")

	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: sessionFile},
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

	fmt.Println("\nStarting authentication process...")
	return client.Run(context.Background(), func(ctx context.Context) error {
		if err := client.Auth().IfNecessary(ctx, auth.NewFlow(&customAuth{phone: phone}, auth.SendCodeOptions{})); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
		fmt.Println("\nSuccessfully logged in!")
		return nil
	})
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		printHelp()
		os.Exit(map[bool]int{true: 1, false: 0}[len(os.Args) < 2])
	}

	sessionFile := getSessionPath()
	var usernames []string

	switch os.Args[1] {
	case "login":
		if len(os.Args) >= 3 {
			sessionFile = os.Args[2]
		}
		fmt.Printf("Logging in to Telegram API (session will be saved to %s)\n", sessionFile)
		if err := login(sessionFile); err != nil {
			fmt.Printf("ERROR: %v\n", err)
			os.Exit(1)
		}
		return

	case "-f", "--file":
		if len(os.Args) < 3 {
			fmt.Println("Error: Missing file path\nUsage: tgun -f <file_with_usernames> [session_file]")
			os.Exit(1)
		}
		if len(os.Args) >= 4 {
			sessionFile = os.Args[3]
		}
		fmt.Printf("Loading usernames from file: %s\n", os.Args[2])
		var err error
		usernames, err = loadUsernamesFromFile(os.Args[2])
		if err != nil {
			fmt.Printf("ERROR: Failed to load usernames from file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Loaded %d usernames from file\n", len(usernames))

	default:
		if len(os.Args) >= 3 {
			sessionFile = os.Args[2]
		}
		usernames = []string{os.Args[1]}
	}

	results, err := checkUsernames(usernames, sessionFile)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}

	var available, taken, errors int
	var availableUsernames []string

	for _, result := range results {
		if result.Error != nil {
			fmt.Printf("[ERROR] %s: %v\n", result.Username, result.Error)
			errors++
		} else if result.Available {
			fmt.Printf("[AVAILABLE] %s\n", result.Username)
			available++
			availableUsernames = append(availableUsernames, result.Username)
		} else {
			fmt.Printf("[TAKEN] %s\n", result.Username)
			taken++
		}
	}

	fmt.Printf("\nSummary:\nTotal: %d, Available: %d, Taken: %d, Errors: %d\n", len(results), available, taken, errors)

	if available > 0 {
		if f, err := os.Create("available_usernames.txt"); err == nil {
			defer f.Close()
			for _, username := range availableUsernames {
				f.WriteString(username + "\n")
			}
			fmt.Println("Available usernames written to available_usernames.txt")
		} else {
			fmt.Printf("ERROR: Failed to create output file: %v\n", err)
		}
	}
}
