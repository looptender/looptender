package main

import (
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
)

// A loop has one display name, the same on every surface: status and
// doctor, client status, panel list, the dock report, and through them the
// BB card, the menu and the page. It is made here and only here (namingOf);
// no surface composes its own. It belongs to the line of work, not to a
// thread or a slug:
//
//   - Explicit custom names and ambiguous legacy inherited names stay pinned.
//   - Automatically derived names follow the last recorded BB title only
//     when it belongs to the current target thread. Provider/model suffixes
//     are preserved as title metadata, not asserted as live execution state.
//   - New auto-inherited task names are temporary until the successor's
//     title is observed. Otherwise the registered slug is the fallback.
//
// Identifier-shaped tokens in it (paths, IDs, URLs, addresses) are shown as
// "[hidden]", as hold notes are on the page. Slug is always the registered
// name, which commands use; Project the group: the directory name of the
// first repository the loop tracks. ShortName is DisplayName cut by the one
// truncation rule (shortName). Nothing renames a loop or a thread.
type loopNaming struct {
	DisplayName string `json:"displayName"`
	// NameSource identifies pinned, title-derived or inherited provenance,
	// or slug when no applicable display name is available.
	NameSource string `json:"nameSource"`
	// ShortName is absent when it is the display name itself.
	ShortName string `json:"shortName,omitempty"`
	// Slug is the registered name; absent in the dock report and panel
	// list, whose loops carry it as name already (inList).
	Slug    string `json:"slug,omitempty"`
	Project string `json:"project,omitempty"`
	// TitleDiffers, for a loop on a BB thread whose title is recorded, is
	// whether that title differs from the display name: data only.
	TitleDiffers bool `json:"titleDiffers,omitempty"`
	// ThreadTitle is the current thread's title, unmasked, with
	// ThreadTitleReadAt when it was read; only where the surface carries
	// private text (status, doctor, client status --private, the page with
	// identifiers).
	ThreadTitle       string     `json:"threadTitle,omitempty"`
	ThreadTitleReadAt *time.Time `json:"threadTitleReadAt,omitempty"`
}

// shortNameMax is the most characters a short name has.
const shortNameMax = 32

// shortName cuts a name at the end, never in the middle, so its beginning,
// which tells loops apart, is kept whole: it keeps the first shortNameMax-1
// characters and adds "…", backing up to just before the last "-", "_",
// ".", ":" or space in the kept part when that loses at most 8 characters,
// so a word is not cut through. A name that fits is unchanged. A UI with
// less room cuts the same way: from the end, with "…".
func shortName(name string) string {
	if utf8.RuneCountInString(name) <= shortNameMax {
		return name
	}
	r := []rune(name)[:shortNameMax-1]
	for i := len(r) - 1; i >= len(r)-8 && i > 0; i-- {
		if strings.ContainsRune("-_.: ", r[i]) {
			r = r[:i]
			break
		}
	}
	return strings.TrimRight(string(r), "-_.: ") + "…"
}

// namingOf is the loop's naming, for loop l whose current target's BB thread
// is thread (empty when the current target is not a BB thread).
func namingOf(l config.Loop, thread string) loopNaming {
	n := namingFallback(l.Name)
	n.ShortName = ""
	if len(l.Progress.Repositories) > 0 {
		if p := filepath.Base(filepath.Clean(l.Progress.Repositories[0])); p != "." && p != string(filepath.Separator) {
			n.Project = state.CleanLaneTitle(p)
		}
	}
	dn, nameErr := state.LoadDisplayName(l.StateDir)
	auto := nameErr == nil && (dn == nil || state.AutoDisplayNameSource(dn.Source))
	if nameErr == nil && dn != nil && (!auto || dn.Source == state.DisplayNameInheritedTitle) {
		if masked, _ := dockMaskNote(dn.Name); strings.TrimSpace(masked) != "" {
			n.DisplayName, n.NameSource = masked, dn.Source
		}
	}
	if thread != "" {
		if t, err := state.LoadThreadTitle(l.StateDir); err == nil && t != nil && t.Thread == thread {
			at := t.ReadAt
			n.ThreadTitle, n.ThreadTitleReadAt = t.Title, &at
			if auto {
				if masked, _ := dockMaskNote(state.CleanDisplayName(t.Title)); strings.TrimSpace(masked) != "" {
					n.DisplayName, n.NameSource = masked, state.DisplayNameThreadTitle
				}
			}
			masked, _ := dockMaskNote(t.Title)
			n.TitleDiffers = masked != n.DisplayName
		}
	}
	if s := shortName(n.DisplayName); s != n.DisplayName {
		n.ShortName = s
	}
	return n
}

// withoutPrivate is the naming without the unmasked title.
func (n loopNaming) withoutPrivate() loopNaming {
	n.ThreadTitle, n.ThreadTitleReadAt = "", nil
	return n
}

// nameLine is how CLI text names a loop: the display name, then the slug
// when it differs.
func (n loopNaming) nameLine() string {
	if n.DisplayName == n.Slug {
		return n.Slug
	}
	return n.DisplayName + " (loop " + n.Slug + ")"
}

// namingFallback is the naming of a loop whose records could not be read:
// its slug.
func namingFallback(slug string) loopNaming {
	n := loopNaming{DisplayName: slug, NameSource: "slug", Slug: slug}
	if s := shortName(slug); s != slug {
		n.ShortName = s
	}
	return n
}

// withSlug is n with its slug: the list surfaces leave it out, since their
// loops carry it as name, and text needs it to tell two loops apart.
func (n loopNaming) withSlug(slug string) loopNaming {
	n.Slug = slug
	return n
}

// listNaming is the naming as the dock report and panel list carry it: no
// unmasked title unless private text is shown, no slug (their loops carry it
// as name), and nothing at all when the display name is the loop's name and
// there is nothing more to say: a reader then shows name. Every list has up
// to dockMaxLoops loops, so a field that repeats what name says is left out
// to keep the response inside its bound.
func listNaming(name string, n *loopNaming, private bool) *loopNaming {
	if n == nil || n.DisplayName == "" {
		return nil
	}
	c := *n
	if !private {
		c = c.withoutPrivate()
	}
	c.Slug = ""
	if c.DisplayName == name && (c.NameSource == "slug" || c.NameSource == "") && c.Project == "" && c.ShortName == "" && !c.TitleDiffers && c.ThreadTitle == "" {
		return nil
	}
	return &c
}

// listNameLine is how list text names a loop: the display name, and the
// slug when they differ, so two loops shown by one name stay apart.
func listNameLine(name string, n *loopNaming) string {
	if n == nil {
		return name
	}
	return n.withSlug(name).nameLine()
}

// namingForConfig resolves the current configured target from saved state,
// without observing BB. The display-name command and succession use the same
// identity binding as status and client snapshots.
func namingForConfig(cfg *config.Config) loopNaming {
	thread := ""
	st, err := state.Load(cfg.Loop.StateDir)
	if err == nil {
		a := cfg.Loop.Agent
		known := st.Target() == state.Primary
		if !known {
			if c, ok := effectiveRoutes(cfg, st).Find(st.Target()); ok && !c.Disabled {
				a, known = st.CandidateAgent(c)
			}
		}
		if known && a.Adapter == "bb" && a.BB != nil {
			thread = a.BB.Thread
		}
	}
	return namingOf(cfg.Loop, thread)
}
