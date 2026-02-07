package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

type rootOptions struct {
	json bool
}

type mutationFlags struct {
	dryRun  bool
	execute bool
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	opts := &rootOptions{}

	cmd := &cobra.Command{
		Use:   "cronlab",
		Short: "Cron lifecycle management",
		Long:  "CronLab validates, tests, reviews, deploys, and audits OpenClaw crons.",
	}
	cmd.PersistentFlags().BoolVar(&opts.json, "json", false, "emit structured JSON output")

	cmd.AddCommand(newValidateCmd(opts))
	cmd.AddCommand(newTestCmd(opts))
	cmd.AddCommand(newReviewCmd(opts))
	cmd.AddCommand(newDeployCmd(opts))
	cmd.AddCommand(newAuditCmd(opts))
	cmd.AddCommand(newActivityCmd(opts))

	return cmd
}

func addMutationFlags(cmd *cobra.Command, m *mutationFlags) {
	m.dryRun = true
	cmd.Flags().BoolVar(&m.dryRun, "dry-run", true, "preview actions without executing them")
	cmd.Flags().BoolVar(&m.execute, "execute", false, "execute mutations (overrides --dry-run)")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "execute")
}

func resolveDryRun(m mutationFlags) bool {
	if m.execute {
		return false
	}
	return m.dryRun
}

func writeOutput(opts *rootOptions, human string, payload any) {
	if opts.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
		return
	}
	fmt.Println(human)
}

func newValidateCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "validate <definition.yaml>",
		Short: "Validate a cron definition",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			writeOutput(opts, "validate: command wired (implementation pending)", map[string]any{"command": "validate", "definition": args[0], "status": "wired"})
		},
	}
}

func newTestCmd(opts *rootOptions) *cobra.Command {
	var mutation mutationFlags
	var timeout string

	cmd := &cobra.Command{
		Use:   "test <definition.yaml>",
		Short: "Test a cron definition",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			dryRun := resolveDryRun(mutation)
			writeOutput(opts, "test: command wired (implementation pending)", map[string]any{"command": "test", "definition": args[0], "timeout": timeout, "dry_run": dryRun, "status": "wired"})
		},
	}
	cmd.Flags().StringVar(&timeout, "timeout", "2m", "maximum test runtime")
	addMutationFlags(cmd, &mutation)
	return cmd
}

func newReviewCmd(opts *rootOptions) *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "review <test-report.json>",
		Short: "Review a test report with an LLM",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			writeOutput(opts, "review: command wired (implementation pending)", map[string]any{"command": "review", "report": args[0], "model": model, "status": "wired"})
		},
	}
	cmd.Flags().StringVar(&model, "model", "deepseek/deepseek-chat-v3-0324", "reviewer model")
	return cmd
}

func newDeployCmd(opts *rootOptions) *cobra.Command {
	var mutation mutationFlags
	var timeout string
	var model string

	cmd := &cobra.Command{
		Use:   "deploy <definition.yaml>",
		Short: "Deploy a validated cron definition",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			dryRun := resolveDryRun(mutation)
			writeOutput(opts, "deploy: command wired (implementation pending)", map[string]any{"command": "deploy", "definition": args[0], "timeout": timeout, "model": model, "dry_run": dryRun, "status": "wired"})
		},
	}
	cmd.Flags().StringVar(&timeout, "timeout", "2m", "maximum test runtime during deployment gate")
	cmd.Flags().StringVar(&model, "model", "deepseek/deepseek-chat-v3-0324", "reviewer model")
	addMutationFlags(cmd, &mutation)
	return cmd
}

func newAuditCmd(opts *rootOptions) *cobra.Command {
	var mutation mutationFlags
	var fix bool

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Audit active crons",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			dryRun := resolveDryRun(mutation)
			writeOutput(opts, "audit: command wired (implementation pending)", map[string]any{"command": "audit", "fix": fix, "dry_run": dryRun, "status": "wired"})
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "auto-remediate common issues")
	addMutationFlags(cmd, &mutation)
	return cmd
}

func newActivityCmd(opts *rootOptions) *cobra.Command {
	var cronName string
	var status string
	var from string
	var to string

	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Query activity ledger",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			writeOutput(opts, "activity: command wired (implementation pending)", map[string]any{"command": "activity", "cron": cronName, "status": status, "from": from, "to": to, "status_text": "wired"})
		},
	}
	cmd.Flags().StringVar(&cronName, "cron", "", "filter by cron name")
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&from, "from", "", "filter from timestamp (RFC3339)")
	cmd.Flags().StringVar(&to, "to", "", "filter to timestamp (RFC3339)")

	return cmd
}
