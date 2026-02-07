package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/misty-step/cronlab/internal/auditor"
	"github.com/misty-step/cronlab/internal/config"
	"github.com/misty-step/cronlab/internal/definition"
	"github.com/misty-step/cronlab/internal/deployer"
	"github.com/misty-step/cronlab/internal/gateway"
	"github.com/misty-step/cronlab/internal/ledger"
	"github.com/misty-step/cronlab/internal/reviewer"
	"github.com/misty-step/cronlab/internal/tester"
	"github.com/spf13/cobra"
)

type rootOptions struct {
	json       bool
	configPath string
	logger     *slog.Logger
}

type mutationFlags struct {
	dryRun  bool
	execute bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	opts := &rootOptions{}

	cmd := &cobra.Command{
		Use:          "cronlab",
		Short:        "Cron lifecycle management",
		Long:         "CronLab validates, tests, reviews, deploys, and audits OpenClaw crons.",
		SilenceUsage: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			opts.logger = buildLogger(opts.json)
			slog.SetDefault(opts.logger)
		},
	}
	cmd.PersistentFlags().BoolVar(&opts.json, "json", false, "emit structured JSON output")
	cmd.PersistentFlags().StringVar(&opts.configPath, "config", config.DefaultPath(), "path to CronLab config file")

	cmd.AddCommand(newValidateCmd(opts))
	cmd.AddCommand(newTestCmd(opts))
	cmd.AddCommand(newReviewCmd(opts))
	cmd.AddCommand(newDeployCmd(opts))
	cmd.AddCommand(newAuditCmd(opts))
	cmd.AddCommand(newActivityCmd(opts))

	return cmd
}

func buildLogger(jsonLogs bool) *slog.Logger {
	handlerOptions := &slog.HandlerOptions{Level: slog.LevelWarn}
	if jsonLogs {
		return slog.New(slog.NewJSONHandler(os.Stderr, handlerOptions))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, handlerOptions))
}

func loadConfig(opts *rootOptions) (config.Config, error) {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return config.Config{}, err
	}
	return cfg, nil
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

func writeOutput(cmd *cobra.Command, opts *rootOptions, human string, payload any) error {
	if opts.json {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), human)
	return err
}

func newGatewayClient(cfg config.Config, logger *slog.Logger) (gateway.GatewayClient, error) {
	return gateway.NewHTTPClient(cfg.Gateway.URL, gateway.WithLogger(logger))
}

func newReviewAdapter(cfg config.Config, logger *slog.Logger) (*reviewAdapter, error) {
	options := []reviewer.OpenRouterOption{reviewer.WithLogger(logger)}
	if apiURL := strings.TrimSpace(os.Getenv("OPENROUTER_API_URL")); apiURL != "" {
		options = append(options, reviewer.WithAPIURL(apiURL))
	}
	client, err := reviewer.NewOpenRouterClientFromEnv(options...)
	if err != nil {
		return nil, err
	}
	return &reviewAdapter{
		client:        client,
		defaultModel:  cfg.Reviewer.Model,
		fallbackModel: cfg.Reviewer.Fallback,
	}, nil
}

func newValidateCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "validate <definition.yaml>",
		Short: "Validate a cron definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := definition.ParseFile(args[0])
			if err != nil {
				return err
			}
			validationErrors := definition.Validate(def)
			if len(validationErrors) > 0 {
				payload := map[string]any{
					"command":    "validate",
					"definition": args[0],
					"valid":      false,
					"errors":     validationErrors,
				}
				human := fmt.Sprintf("INVALID: %d validation error(s)", len(validationErrors))
				if err := writeOutput(cmd, opts, human, payload); err != nil {
					return err
				}
				if !opts.json {
					for _, vErr := range validationErrors {
						fmt.Fprintf(cmd.OutOrStdout(), "- %s: %s\n", vErr.Field, vErr.Message)
					}
				}
				return errors.New("validation failed")
			}

			payload := map[string]any{
				"command":    "validate",
				"definition": args[0],
				"valid":      true,
				"name":       def.Name,
			}
			human := fmt.Sprintf("VALID: %s", def.Name)
			return writeOutput(cmd, opts, human, payload)
		},
	}
}

func newTestCmd(opts *rootOptions) *cobra.Command {
	var mutation mutationFlags
	var timeoutRaw string
	var reportPath string

	cmd := &cobra.Command{
		Use:   "test <definition.yaml>",
		Short: "Test a cron definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := definition.ParseFile(args[0])
			if err != nil {
				return err
			}

			timeout, err := time.ParseDuration(timeoutRaw)
			if err != nil {
				return fmt.Errorf("invalid --timeout value: %w", err)
			}

			dryRun := resolveDryRun(mutation)

			var gw gateway.GatewayClient
			if !dryRun {
				cfg, err := loadConfig(opts)
				if err != nil {
					return err
				}
				gw, err = newGatewayClient(cfg, opts.logger)
				if err != nil {
					return err
				}
			}

			runner := tester.NewRunner(gw, opts.logger)
			report, runErr := runner.Run(cmd.Context(), *def, tester.Options{Timeout: timeout, DryRun: dryRun})

			if strings.TrimSpace(reportPath) == "" {
				reportPath = defaultReportPath(args[0])
			}
			if err := tester.SaveReport(reportPath, report); err != nil {
				return err
			}

			payload := map[string]any{
				"command":     "test",
				"definition":  args[0],
				"report_path": reportPath,
				"report":      report,
			}
			human := fmt.Sprintf("%s: %s (report: %s)", report.Status, def.Name, reportPath)
			if err := writeOutput(cmd, opts, human, payload); err != nil {
				return err
			}

			if runErr != nil {
				return runErr
			}
			if !report.Passed {
				return errors.New("test failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&timeoutRaw, "timeout", "2m", "maximum test runtime")
	cmd.Flags().StringVar(&reportPath, "report", "", "path to write test report JSON")
	addMutationFlags(cmd, &mutation)
	return cmd
}

func defaultReportPath(definitionPath string) string {
	base := filepath.Base(definitionPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name == "" {
		name = "cron"
	}
	return name + ".test-report.json"
}

func newReviewCmd(opts *rootOptions) *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "review <test-report.json>",
		Short: "Review a test report with an LLM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := tester.LoadReport(args[0])
			if err != nil {
				return err
			}
			cfg, err := loadConfig(opts)
			if err != nil {
				return err
			}

			reviewerClient, err := newReviewAdapter(cfg, opts.logger)
			if err != nil {
				return err
			}

			result, err := reviewerClient.Review(cmd.Context(), report, reviewer.Options{Model: model})
			if err != nil {
				return err
			}

			payload := map[string]any{
				"command": "review",
				"report":  args[0],
				"result":  result,
			}
			human := fmt.Sprintf("%s: %s", result.Status, result.Commentary)
			if err := writeOutput(cmd, opts, human, payload); err != nil {
				return err
			}
			if !result.Passed {
				return errors.New("review failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "reviewer model (defaults to config reviewer.model)")
	return cmd
}

func newDeployCmd(opts *rootOptions) *cobra.Command {
	var mutation mutationFlags
	var timeoutRaw string
	var model string

	cmd := &cobra.Command{
		Use:   "deploy <definition.yaml>",
		Short: "Deploy a validated cron definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts)
			if err != nil {
				return err
			}

			def, err := definition.ParseFile(args[0])
			if err != nil {
				return err
			}

			timeout, err := time.ParseDuration(timeoutRaw)
			if err != nil {
				return fmt.Errorf("invalid --timeout value: %w", err)
			}

			gw, err := newGatewayClient(cfg, opts.logger)
			if err != nil {
				return err
			}
			activity := ledger.New(cfg.Activity.Path)
			runner := tester.NewRunner(gw, opts.logger)
			rev, err := newReviewAdapter(cfg, opts.logger)
			if err != nil {
				return err
			}

			d := deployer.New(gw, runner, rev, activity, opts.logger)
			res, err := d.Deploy(cmd.Context(), *def, deployer.Options{
				DryRun:  resolveDryRun(mutation),
				Timeout: timeout,
				Model:   model,
			})

			payload := map[string]any{
				"command": "deploy",
				"result":  res,
			}
			human := fmt.Sprintf("deploy action=%s cron=%s dry_run=%t", res.Action, res.Definition, res.DryRun)
			if errOut := writeOutput(cmd, opts, human, payload); errOut != nil {
				return errOut
			}

			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&timeoutRaw, "timeout", "2m", "maximum test runtime during deployment gate")
	cmd.Flags().StringVar(&model, "model", "", "reviewer model (defaults to config reviewer.model)")
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
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts)
			if err != nil {
				return err
			}
			gw, err := newGatewayClient(cfg, opts.logger)
			if err != nil {
				return err
			}
			activity := ledger.New(cfg.Activity.Path)
			a := auditor.New(gw, activity, opts.logger)

			auditOpts := auditor.DefaultOptions()
			auditOpts.Fix = fix
			auditOpts.DryRun = resolveDryRun(mutation)
			report, err := a.Audit(cmd.Context(), auditOpts)
			if err != nil {
				return err
			}

			payload := map[string]any{
				"command": "audit",
				"report":  report,
			}
			human := fmt.Sprintf("audit complete: total=%d healthy=%d warning=%d critical=%d", report.Total, report.Healthy, report.Warning, report.Critical)
			if err := writeOutput(cmd, opts, human, payload); err != nil {
				return err
			}
			if !opts.json {
				for _, cron := range report.Crons {
					fmt.Fprintf(cmd.OutOrStdout(), "- [%s] %s\n", strings.ToUpper(string(cron.Severity)), cron.Cron.Name)
					for _, issue := range cron.Issues {
						fmt.Fprintf(cmd.OutOrStdout(), "  - %s: %s\n", issue.Code, issue.Message)
					}
				}
			}
			if report.Critical > 0 {
				return errors.New("critical cron health issues detected")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "auto-remediate common issues")
	addMutationFlags(cmd, &mutation)
	return cmd
}

func newActivityCmd(opts *rootOptions) *cobra.Command {
	var cronName string
	var status string
	var fromRaw string
	var toRaw string
	var limit int

	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Query activity ledger",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts)
			if err != nil {
				return err
			}
			store := ledger.New(cfg.Activity.Path)

			from, err := parseRFC3339Ptr(fromRaw)
			if err != nil {
				return err
			}
			to, err := parseRFC3339Ptr(toRaw)
			if err != nil {
				return err
			}

			entries, err := store.Query(cmd.Context(), ledger.QueryFilter{
				Cron:   cronName,
				Status: status,
				From:   from,
				To:     to,
				Limit:  limit,
			})
			if err != nil {
				return err
			}

			stats := ledger.ComputeStats(entries)
			payload := map[string]any{
				"command": "activity",
				"count":   len(entries),
				"stats":   stats,
				"entries": entries,
			}
			human := fmt.Sprintf("activity rows=%d success_rate=%.2f failures=%d", len(entries), stats.SuccessRate, stats.FailureCount)
			if err := writeOutput(cmd, opts, human, payload); err != nil {
				return err
			}

			if !opts.json {
				for _, e := range entries {
					fmt.Fprintf(cmd.OutOrStdout(), "- %s %s %s %dms\n", e.Timestamp.Format(time.RFC3339), e.Cron, e.Status, e.DurationMS)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cronName, "cron", "", "filter by cron name")
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&fromRaw, "from", "", "filter from timestamp (RFC3339)")
	cmd.Flags().StringVar(&toRaw, "to", "", "filter to timestamp (RFC3339)")
	cmd.Flags().IntVar(&limit, "limit", 200, "maximum rows to return")

	return cmd
}

func parseRFC3339Ptr(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("invalid RFC3339 timestamp %q: %w", value, err)
	}
	utc := ts.UTC()
	return &utc, nil
}

type reviewAdapter struct {
	client        reviewer.Client
	defaultModel  string
	fallbackModel string
}

func (a *reviewAdapter) Review(ctx context.Context, report tester.Report, opts reviewer.Options) (reviewer.Result, error) {
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = strings.TrimSpace(a.defaultModel)
	}
	result, err := a.client.Review(ctx, report, reviewer.Options{Model: model})
	if err == nil {
		return result, nil
	}
	fallback := strings.TrimSpace(a.fallbackModel)
	if fallback == "" || fallback == model {
		return result, err
	}
	return a.client.Review(ctx, report, reviewer.Options{Model: fallback})
}
