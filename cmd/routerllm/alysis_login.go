package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"routerllm/internal/alysis"
)

func runAlysisLogin() error {
	path := alysis.DefaultAccountsPath()
	store, err := alysis.LoadAccountStore(path)
	if err != nil {
		return err
	}

	client := &alysis.Client{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	clientName := "routerllm"
	if hostname, err := os.Hostname(); err == nil {
		clientName = "routerllm @ " + hostname
	}
	device, err := client.RequestDeviceAuth(ctx, clientName)
	if err != nil {
		return err
	}

	fmt.Println("open this URL to authorize RouterLLM:")
	fmt.Println("  " + client.ActivationURL(device))
	fmt.Println("user code: " + device.UserCode)
	fmt.Println("waiting for authorization...")

	key, err := client.PollForToken(ctx, device)
	if err != nil {
		return err
	}

	models, err := client.VerifyKey(ctx, key)
	if err != nil {
		fmt.Printf("warning: could not verify key: %v\n", err)
	} else {
		fmt.Printf("verified — %d models available:\n", len(models))
		for _, model := range models {
			fmt.Println("  - " + model)
		}
	}

	account := alysis.Account{
		AccountID:  "acc_" + strconv.FormatInt(time.Now().UnixMilli(), 10),
		GatewayKey: key,
		CreatedAt:  time.Now(),
	}
	if err := store.Add(account); err != nil {
		return err
	}

	fmt.Printf("alysis account added: %s (%s)\n", account.AccountID, keyHint(key))
	fmt.Printf("credentials stored in %s\n", store.Path())

	return nil
}

func keyHint(key string) string {
	if len(key) < 5 {
		return "..."
	}

	return fmt.Sprintf("...%s", key[len(key)-4:])
}
