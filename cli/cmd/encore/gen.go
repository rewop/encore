package main

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	daemonpb "encr.dev/proto/encore/daemon"
	"github.com/spf13/cobra"
)

func init() {
	genCmd := &cobra.Command{
		Use:   "gen",
		Short: "Code generation commands",
	}
	rootCmd.AddCommand(genCmd)

	var (
		output  string
		lang    string
		envName string
	)

	genClientCmd := &cobra.Command{
		Use:   "client <app-id>",
		Short: "Generates an API client for your app",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if output == "" && lang == "" {
				fatal("specify at least one of --output or --lang.")
			}
			appID := args[0]

			if lang == "" {
				var ok bool
				lang, ok = detectLang(output)
				if !ok {
					fatal("could not detect language from output.\n\nNote: you can specify the language explicitly with --lang.")
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			daemon := setupDaemon(ctx)
			resp, err := daemon.GenClient(ctx, &daemonpb.GenClientRequest{
				AppId:   appID,
				EnvName: envName,
				Lang:    lang,
			})
			if err != nil {
				fatal(err)
			}

			if output == "" {
				os.Stdout.Write(resp.Code)
			} else {
				if err := ioutil.WriteFile(output, resp.Code, 0755); err != nil {
					fatal(err)
				}
			}
		},
	}

	genCmd.AddCommand(genClientCmd)
	genClientCmd.Flags().StringVarP(&output, "output", "o", "", "The filename to write the generated client code to")
	genClientCmd.Flags().StringVarP(&lang, "lang", "l", "", "The language to generate code for (only \"ts\" is supported for now)")
	genClientCmd.Flags().StringVarP(&envName, "env", "e", "production", "The environment to fetch the API for")
}

func detectLang(path string) (string, bool) {
	suffix := strings.ToLower(filepath.Ext(path))
	switch suffix {
	case ".ts":
		return "typescript", true
	default:
		return "", false
	}
}