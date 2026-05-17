package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(tokenCmd)
}

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage API tokens",
}

func init() {
	createTokenCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new API token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			serverURL, _ := cmd.Flags().GetString("server")
			token, _ := cmd.Flags().GetString("token")
			name, _ := cmd.Flags().GetString("name")
			scopes, _ := cmd.Flags().GetString("scopes")

			body, _ := json.Marshal(map[string]string{
				"name":   name,
				"scopes": scopes,
			})

			req, err := http.NewRequest("POST", serverURL+"/api/v1/tokens", bytes.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			out, _ := io.ReadAll(resp.Body)
			fmt.Println(string(out))
			return nil
		},
	}
	createTokenCmd.Flags().String("server", "", "Registry server URL")
	createTokenCmd.Flags().String("token", "", "Admin API token")
	createTokenCmd.Flags().String("name", "", "Token name")
	createTokenCmd.Flags().String("scopes", "read,write", "Token scopes")
	tokenCmd.AddCommand(createTokenCmd)

	listTokenCmd := &cobra.Command{
		Use:   "list",
		Short: "List API tokens",
		RunE: func(cmd *cobra.Command, _ []string) error {
			serverURL, _ := cmd.Flags().GetString("server")
			token, _ := cmd.Flags().GetString("token")

			req, err := http.NewRequest("GET", serverURL+"/api/v1/tokens", nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+token)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			out, _ := io.ReadAll(resp.Body)
			fmt.Println(string(out))
			return nil
		},
	}
	listTokenCmd.Flags().String("server", "", "Registry server URL")
	listTokenCmd.Flags().String("token", "", "Admin API token")
	tokenCmd.AddCommand(listTokenCmd)
}
