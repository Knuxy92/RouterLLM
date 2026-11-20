package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"routerllm/internal/kilo"
)

func runKiloLogin() error {
	path := kilo.DefaultAccountsPath()
	store, err := kilo.LoadAccountStore(path)
	if err != nil {
		return err
	}

	client := &kilo.Client{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	device, err := client.RequestDeviceAuth(ctx)
	if err != nil {
		return err
	}

	fmt.Println("open this URL to authorize RouterLLM:")
	fmt.Println("  " + device.VerificationURL)
	fmt.Println("user code: " + device.UserCode)
	fmt.Println("waiting for authorization...")

	token, err := client.PollForToken(ctx, device)
	if err != nil {
		return err
	}

	profile, err := client.FetchProfile(ctx, token)
	if err != nil {
		return fmt.Errorf("token rejected: %w", err)
	}

	account := kilo.Account{
		AccountID:      "acc_" + strconv.FormatInt(time.Now().UnixMilli(), 10),
		Email:          profile.User.Email,
		OrganizationID: profile.SelectedOrganizationID,
		Token:          token,
		CreatedAt:      time.Now(),
	}
	if err := store.Add(account); err != nil {
		return err
	}

	fmt.Printf("kilo account added: %s (%s)\n", account.AccountID, keyHint(token))
	if account.Email != "" {
		fmt.Printf("signed in as %s\n", account.Email)
	}
	if account.OrganizationID != "" {
		fmt.Printf("organization: %s\n", account.OrganizationID)
	}
	fmt.Printf("credentials stored in %s\n", store.Path())

	return nil
}
