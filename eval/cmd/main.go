package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/OnslaughtSnail/caelis/eval/cases"
	evalproviders "github.com/OnslaughtSnail/caelis/eval/providers"
	"github.com/OnslaughtSnail/caelis/eval/runner"
	"github.com/OnslaughtSnail/caelis/internal/envload"
)

func main() {
	var (
		suite       = flag.String("suite", "light", "Eval suite: light|nightly")
		model       = flag.String("model", "", "Single model alias to run")
		models      = flag.String("models", "", "Comma-separated model aliases to run")
		stream      = flag.String("stream-modes", "off", "Stream modes: off|on|both")
		thinking    = flag.String("thinking-modes", "off", "Thinking modes: off|on|both")
		thinkBudget = flag.Int("thinking-budget", 1024, "Thinking token budget when mode is on")
		effort      = flag.String("reasoning-effort", "", "Reasoning effort hint: low|medium|high")
		listModels  = flag.Bool("list-models", false, "List supported model aliases")
		listCases   = flag.Bool("list-cases", false, "List eval cases")
	)
	flag.Parse()
	if _, err := envload.LoadNearest(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *listModels {
		for _, m := range evalproviders.ListModels() {
			fmt.Println(m)
		}
		return
	}
	if *listCases {
		selected := cases.Light()
		if strings.EqualFold(*suite, "nightly") {
			selected = cases.Nightly()
		}
		for _, c := range selected {
			fmt.Printf("%s: %s\n", c.Name, c.Description)
		}
		return
	}

	summary, err := runner.Run(context.Background(), runner.Options{
		Suite:           *suite,
		Model:           *model,
		Models:          *models,
		StreamModes:     *stream,
		ThinkingModes:   *thinking,
		ThinkingBudget:  *thinkBudget,
		ReasoningEffort: *effort,
	})
	if summary != nil {
		fmt.Printf("suite=%s passed=%d failed=%d\n", summary.Suite, summary.Passed, summary.Failed)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
