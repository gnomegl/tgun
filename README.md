# tgun - Telegram Username Checker

Telegram username availability checker that actually works. Check single usernames or blast through wordlists to claim those sweet handles before the zoomers get them.

## Install

```bash
go install github.com/yourusername/tgun
```

## Setup

Get your Telegram API credentials from https://my.telegram.org 

```bash
tgun login
```

## Usage

Check one username:
```bash
tgun username123
```

Check from file:
```bash
tgun -f usernames.txt
```

## Examples

```bash
# Single check
tgun coolguy2024

# Batch check
echo -e "admin\nroot\ntest" > targets.txt
tgun -f targets.txt

```

Available usernames get dumped to `available_usernames.txt`. Files support comments with `#`.

## Notes

- Rate limited to not get your account nuked. It goes much slower than it needs to. 
- Requires 2FA if you have it enabled

That's it. Go claim some handles.
