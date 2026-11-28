package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/prompt"
)

func TestInitPreservesTaskBytesThroughCreation(t *testing.T) {
	tasks := []string{
		" \t\nFictional task 界🙂 with \"quotes\" and \\ slash\n  indented acceptance\n\t ",
		"Fictional task without a final newline",
		"\r\n\tFictional task\r\n\r\n",
	}
	for _, source := range []string{"stdin", "file", "flag"} {
		for _, withGoal := range []bool{false, true} {
			for i, task := range tasks {
				t.Run(fmt.Sprintf("%s/goal-%t/task-%d", source, withGoal, i), func(t *testing.T) {
					f := newSetupFixture(t)
					args := append(f.args(), "--json", fmt.Sprintf("--with-goal=%t", withGoal))
					input := ""
					switch source {
					case "stdin":
						args = append(args, "--task", "", "--task-file", "-")
						input = task
					case "file":
						path := filepath.Join(f.root, "input.md")
						if err := os.WriteFile(path, []byte(task), 0600); err != nil {
							t.Fatal(err)
						}
						args = append(args, "--task", "", "--task-file", path)
					default:
						args = append(args, "--task", task)
					}
					code, out, errOut := f.run(t, append(args, "--dry-run"), input)
					if code != 0 {
						t.Fatalf("preview: %d %s", code, errOut)
					}
					preview := decodeInitReport(t, out)
					if _, err := os.Stat(f.destination); !os.IsNotExist(err) {
						t.Fatal("preview wrote destination")
					}
					code, out, errOut = f.run(t, args, input)
					if code != 0 {
						t.Fatalf("create: %d %s", code, errOut)
					}
					created := decodeInitReport(t, out)
					if created.TaskPath != preview.TaskPath || created.TaskBinding != preview.TaskBinding {
						t.Fatal("snapshot plan changed")
					}
					assertInitTaskBytes(t, f, task, withGoal)
				})
			}
		}
	}
}

// Read the artifact itself: preview metadata cannot prove saved task fidelity.
func assertInitTaskBytes(t *testing.T, f setupFixture, task string, withGoal bool) {
	t.Helper()
	name, want := "task.md", []byte(task)
	if !withGoal {
		name = "prompt.md"
		// Existing generated policy and its final newline are framing. Every
		// byte between them belongs to the task and must survive unchanged.
		want = []byte(prompt.PolicyHead(initCommandPrefix(), commandQuote(filepath.Join(f.destination, config.FileName))) + task + "\n")
	}
	saved, err := os.ReadFile(filepath.Join(f.destination, name))
	if err != nil || !bytes.Equal(saved, want) {
		t.Fatalf("task bytes changed: got %d bytes, want %d (%v)", len(saved), len(want), err)
	}
}

func TestInitGoalTaskBoundaryCountsOriginalBytes(t *testing.T) {
	// Multibyte content plus significant trailing whitespace: trimming first
	// must not let an over-limit task through or shrink an accepted snapshot.
	exact := " " + strings.Repeat("界", (initTaskLimit-4)/3) + "\n\t "
	if len(exact) != initTaskLimit {
		t.Fatal("invalid boundary fixture")
	}
	for _, source := range []string{"stdin", "file", "flag"} {
		for _, task := range []string{exact, exact + " ", " \t\r\n"} {
			t.Run(fmt.Sprintf("%s/%d", source, len(task)), func(t *testing.T) {
				f := newSetupFixture(t)
				args := append(f.args(), "--json", "--with-goal")
				input := ""
				switch source {
				case "stdin":
					args = append(args, "--task", "", "--task-file", "-")
					input = task
				case "file":
					path := filepath.Join(f.root, "input.md")
					if err := os.WriteFile(path, []byte(task), 0600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--task", "", "--task-file", path)
				default:
					args = append(args, "--task", task)
				}
				code, _, errOut := f.run(t, args, input)
				if task == exact {
					if code != 0 {
						t.Fatalf("exact bound refused: %s", errOut)
					}
					assertInitTaskBytes(t, f, task, true)
				} else {
					if code == 0 {
						t.Fatal("invalid original task accepted")
					}
					if _, err := os.Stat(f.destination); !os.IsNotExist(err) {
						t.Fatal("refusal wrote destination")
					}
				}
			})
		}
	}
}

func TestInitWizardTaskPreservesAnswerWhitespace(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		for _, withGoal := range []bool{false, true} {
			t.Run(fmt.Sprintf("ending-%q/goal-%t", ending, withGoal), func(t *testing.T) {
				f := newSetupFixture(t)
				task := " \tFictional 界🙂 task with indentation\t "
				args := append(f.args(), "--non-interactive=false", "--task", "", fmt.Sprintf("--with-goal=%t", withGoal))
				// Blank task-file answer, one task answer, then existing defaults.
				input := ending + task + ending + strings.Repeat(ending, 16)
				code, _, errOut := f.run(t, args, input)
				if code != 0 {
					t.Fatalf("wizard: %d %s", code, errOut)
				}
				assertInitTaskBytes(t, f, task, withGoal)
			})
		}
	}
}
