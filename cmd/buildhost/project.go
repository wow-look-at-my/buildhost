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
	rootCmd.AddCommand(projectCmd)
}

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage projects",
}

func init() {
	createProjectCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			serverURL, _ := cmd.Flags().GetString("server")
			token, _ := cmd.Flags().GetString("token")
			name, _ := cmd.Flags().GetString("name")
			description, _ := cmd.Flags().GetString("description")
			private, _ := cmd.Flags().GetBool("private")
			versioning, _ := cmd.Flags().GetString("versioning")

			body, _ := json.Marshal(map[string]any{
				"name":        name,
				"description": description,
				"is_private":  private,
				"versioning":  versioning,
			})

			req, err := http.NewRequest("POST", serverURL+"/api/v1/projects", bytes.NewReader(body))
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
	createProjectCmd.Flags().String("server", "", "Registry server URL")
	createProjectCmd.Flags().String("token", "", "API token")
	createProjectCmd.Flags().String("name", "", "Project name")
	createProjectCmd.Flags().String("description", "", "Project description")
	createProjectCmd.Flags().Bool("private", false, "Private project")
	createProjectCmd.Flags().String("versioning", "auto", "Versioning scheme (auto or semver)")
	projectCmd.AddCommand(createProjectCmd)

	listProjectCmd := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		RunE: func(cmd *cobra.Command, _ []string) error {
			serverURL, _ := cmd.Flags().GetString("server")
			token, _ := cmd.Flags().GetString("token")

			req, err := http.NewRequest("GET", serverURL+"/api/v1/projects", nil)
			if err != nil {
				return err
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}

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
	listProjectCmd.Flags().String("server", "", "Registry server URL")
	listProjectCmd.Flags().String("token", "", "API token (optional)")
	projectCmd.AddCommand(listProjectCmd)
}
