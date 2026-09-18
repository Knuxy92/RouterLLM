package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"routerllm/internal/codex"
)

func runCodexLogin() error {
	path := codex.DefaultAccountsPath()
	store, err := codex.LoadAccountStore(path)
	if err != nil {
		return err
	}

	manager := codex.NewManager(&codex.Client{HTTPClient: &http.Client{Timeout: 30 * time.Second}}, store)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	account, err := manager.Login(ctx, func(device codex.DeviceAuth) {
		url := device.VerificationURIComplete
		if url == "" {
			url = device.VerificationURI
		}

		fmt.Println("open this URL to authorize RouterLLM:")
		fmt.Println("  " + url)
		fmt.Println("user code: " + device.UserCode)
		fmt.Println("waiting for authorization...")
	})
	if err != nil {
		return err
	}

	email := account.Email
	if email == "" {
		email = "unknown"
	}
	fmt.Printf("codex account added: %s (%s)\n", email, account.AccountID)
	fmt.Printf("credentials stored in %s\n", store.Path())

	return nil
}
