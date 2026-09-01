package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"routerllm/internal/cline"
)

func runClineLogin() error {
	path := cline.DefaultAccountsPath()
	store, err := cline.LoadAccountStore(path)
	if err != nil {
		return err
	}

	manager := cline.NewManager(&cline.Client{HTTPClient: &http.Client{Timeout: 30 * time.Second}}, store)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	account, err := manager.Login(ctx, func(device cline.DeviceAuth) {
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
	fmt.Printf("cline account added: %s (%s)\n", email, account.AccountID)
	fmt.Printf("credentials stored in %s\n", store.Path())

	return nil
}
