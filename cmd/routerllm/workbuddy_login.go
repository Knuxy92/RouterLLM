package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"routerllm/internal/workbuddy"
)

func runWorkbuddyLogin() error {
	path := workbuddy.DefaultAccountsPath()
	store, err := workbuddy.LoadAccountStore(path)
	if err != nil {
		return err
	}

	client := &workbuddy.Client{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	state, authURL, err := client.StartLogin(ctx, "intl", "CLI")
	if err != nil {
		return err
	}

	fmt.Println("open this URL to authorize RouterLLM:")
	fmt.Println("  " + authURL)
	fmt.Println("waiting for authorization (approve in the browser, headless-friendly)...")

	token, err := pollWorkbuddyToken(ctx, client, state)
	if err != nil {
		return err
	}

	account := workbuddy.Account{
		UID:          workbuddy.AccountUID(token.AccessToken),
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Realm:        "intl",
		Product:      "desktop",
		CreatedAt:    time.Now(),
		ExpiresAt:    token.ExpiresAt.Unix(),
	}
	if err := store.Add(account); err != nil {
		return err
	}

	label := account.Email
	if label == "" {
		label = account.UID
	}
	if label == "" {
		label = "unknown"
	}
	fmt.Printf("workbuddy account added: %s\n", label)
	fmt.Printf("credentials stored in %s\n", store.Path())

	return nil
}

// pollWorkbuddyToken polls the login state until the access token arrives.
// The upstream answers "login ing..." (code 11217) until the user approves,
// which is a pending state, not a failure.
func pollWorkbuddyToken(ctx context.Context, client *workbuddy.Client, state string) (workbuddy.Token, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		token, pending, err := client.PollLogin(ctx, "intl", state)
		if err != nil {
			return workbuddy.Token{}, err
		}
		if !pending {
			return token, nil
		}

		select {
		case <-ctx.Done():
			return workbuddy.Token{}, fmt.Errorf("workbuddy login expired before authorization completed")
		case <-ticker.C:
		}
	}
}
