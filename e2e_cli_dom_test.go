package main

import (
	"context"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
)

func TestE2ECLIShadowDOMBoundary(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/shadow-dom", "--new", "--wait-for", `[data-shadow-ready="true"]`, "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("shadow DOM open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	// The tree snapshot and its substring selector traverse an open shadow root,
	// while page CSS selectors remain scoped to the document unless eval enters
	// host.shadowRoot explicitly.
	requireEvalBool(t, env, `document.querySelector("#shadow-action-button") === null`, true)
	requireEvalBool(t, env, `document.querySelector("#shadow-host").shadowRoot.mode === "open"`, true)

	selected := runE2EJSON(t, env, "snapshot", "--interactive", "--selector", "shadow-action-button", "--tab", tab, "--json").Data.SnapshotData
	if selected == nil || len(selected.Elements) != 1 || selected.Elements[0].Name != "Shadow action button" {
		t.Fatalf("shadow DOM selector-filtered elements = %+v", selected)
	}

	snapshot := runE2EJSON(t, env, "snapshot", "--interactive", "--tab", tab, "--json").Data.SnapshotData
	if snapshot == nil {
		t.Fatal("shadow DOM snapshot returned no snapshot data")
	}
	requireContains(t, snapshot.Snapshot, "Shadow action button", "shadow DOM snapshot")
	requireContains(t, snapshot.Snapshot, "Shadow text input", "shadow DOM snapshot")
	buttonRef := refByName(t, snapshot, "Shadow action button")
	inputRef := refByName(t, snapshot, "Shadow text input")

	runE2EJSON(t, env, "click", buttonRef, "--tab", tab, "--json")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#shadow-host").shadowRoot.querySelector("#shadow-result").textContent`, "clicked 1")

	runE2EJSON(t, env, "fill", inputRef, "shadow value 純文字", "--tab", tab, "--json")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#shadow-host").shadowRoot.querySelector("#shadow-text-input").value`, "shadow value 純文字")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#shadow-host").shadowRoot.querySelector("#shadow-result").textContent`, "value: shadow value 純文字")
}

func TestE2ECLIAccessibilityState(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/accessibility-state", "--new", "--wait-for", "#accessibility-state-ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("accessibility state open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	initial := runE2EJSON(t, env, "snapshot", "--tab", tab, "--json").Data.SnapshotData
	if initial == nil {
		t.Fatal("initial accessibility state snapshot returned no data")
	}
	for _, want := range []string{
		`button "Disabled action" [disabled]`,
		`button [ref=`,
		`"State disclosure" [expanded=false]`,
		`"State checkbox" [checked=false]`,
		`"Choice one" [selected=true]`,
		`"Choice two" [selected=false]`,
		`"State updates" [live=polite]`,
		`State idle`,
	} {
		requireContains(t, initial.Snapshot, want, "initial accessibility state snapshot")
	}
	requireNotContains(t, initial.Snapshot, "Revealed accessibility details", "initial accessibility state snapshot")
	for _, element := range initial.Elements {
		if element.Name == "Disabled action" {
			t.Fatalf("disabled action unexpectedly received interactive ref %q", element.Ref)
		}
	}
	mutateRef := refByName(t, initial, "Mutate accessibility state")

	runE2EJSON(t, env, "click", mutateRef, "--tab", tab, "--wait-for", `#state-live[data-state="updated"]`, "--timeout", "5000", "--json")
	updated := runE2EJSON(t, env, "snapshot", "--tab", tab, "--json").Data.SnapshotData
	for _, want := range []string{
		`button [ref=`,
		`"Disabled action"`,
		`"State disclosure" [expanded=true]`,
		`Revealed accessibility details`,
		`"State checkbox" [checked=true]`,
		`"Choice one" [selected=false]`,
		`"Choice two" [selected=true]`,
		`Accessibility state updated`,
	} {
		requireContains(t, updated.Snapshot, want, "updated accessibility state snapshot")
	}
	requireNotContains(t, updated.Snapshot, `"Disabled action" [disabled]`, "updated accessibility state snapshot")
	if refByName(t, updated, "Disabled action") == "" {
		t.Fatal("enabled action did not receive a refreshed ref")
	}
	refreshedMutateRef := refByName(t, updated, "Mutate accessibility state")
	if refreshedMutateRef == mutateRef {
		t.Fatalf("mutation ref was not regenerated after interactive state changed: %q", mutateRef)
	}

	runE2EJSON(t, env, "click", refreshedMutateRef, "--tab", tab, "--json")
	reset := runE2EJSON(t, env, "snapshot", "--tab", tab, "--json").Data.SnapshotData
	requireContains(t, reset.Snapshot, `"State disclosure" [expanded=false]`, "reset accessibility state snapshot")
	requireNotContains(t, reset.Snapshot, "Revealed accessibility details", "reset accessibility state snapshot")
}

func TestE2ECLINestedScrolling(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/scrolling", "--new", "--wait-for", `[data-initialized="true"]`, "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("scrolling open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	runE2EJSON(t, env, "viewport", "800x600", "--dpr", "1", "--tab", tab, "--json")
	runE2EJSON(t, env, "eval", `(() => {
      window.scrollTo(0, 0);
      document.querySelector('#outer-scroll').scrollTo(40, 50);
      document.querySelector('#inner-scroll').scrollTo(60, 70);
      return true;
    })()`, "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")

	check := func(label, script string) {
		t.Helper()
		resp := runE2EJSON(t, env, "eval", script, "--tab", tab, "--json")
		if got, ok := resp.Data.Result.(bool); !ok || !got {
			state := runE2EJSON(t, env, "eval", `(() => {
          const root = document.scrollingElement;
          const outer = document.querySelector('#outer-scroll');
          const inner = document.querySelector('#inner-scroll');
          const marker = document.querySelector('#viewport-end-marker').getBoundingClientRect();
          return {
            x: scrollX, y: scrollY,
            maxX: root.scrollWidth - innerWidth, maxY: root.scrollHeight - innerHeight,
            outerX: outer.scrollLeft, outerY: outer.scrollTop,
            innerX: inner.scrollLeft, innerY: inner.scrollTop,
            marker: { left: marker.left, top: marker.top, right: marker.right, bottom: marker.bottom }
          };
        })()`, "--tab", tab, "--json")
			t.Fatalf("%s state check = %#v, want true; positions = %#v", label, resp.Data.Result, state.Data.Result)
		}
	}
	nestedUnchanged := `
      const outer = document.querySelector('#outer-scroll');
      const inner = document.querySelector('#inner-scroll');
      const nestedStable = outer.scrollLeft === 40 && outer.scrollTop === 50 && inner.scrollLeft === 60 && inner.scrollTop === 70;`
	check("initial", `(() => {`+nestedUnchanged+`
      const marker = document.querySelector('#viewport-end-marker').getBoundingClientRect();
      const markerVisible = marker.left >= 0 && marker.top >= 0 && marker.right <= innerWidth && marker.bottom <= innerHeight;
      return scrollX === 0 && scrollY === 0 && nestedStable && !markerVisible;
    })()`)

	// The default distance is 300px; the remaining directional commands use
	// explicit distances so both parsing paths and every axis are exercised.
	runE2EJSON(t, env, "scroll", "down", "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	runE2EJSON(t, env, "scroll", "right", "220", "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	runE2EJSON(t, env, "scroll", "up", "125", "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	runE2EJSON(t, env, "scroll", "left", "70", "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	check("directional pixels", `(() => {`+nestedUnchanged+`
      return scrollX === 150 && scrollY === 175 && nestedStable;
    })()`)

	for _, direction := range []string{"down", "right"} {
		runE2EJSON(t, env, "scroll", direction, "10000", "--tab", tab, "--json")
		runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	}
	check("maximum", `(() => {`+nestedUnchanged+`
      const root = document.scrollingElement;
      const marker = document.querySelector('#viewport-end-marker').getBoundingClientRect();
      const markerVisible = marker.left >= 0 && marker.top >= 0 && marker.right <= innerWidth && marker.bottom <= innerHeight;
      return scrollX === root.scrollWidth - innerWidth && scrollY === root.scrollHeight - innerHeight && nestedStable && markerVisible;
    })()`)
	for _, direction := range []string{"down", "right"} {
		runE2EJSON(t, env, "scroll", direction, "10000", "--tab", tab, "--json")
		runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	}
	check("stable maximum", `(() => {
      const root = document.scrollingElement;
      return scrollX === root.scrollWidth - innerWidth && scrollY === root.scrollHeight - innerHeight;
    })()`)

	for _, direction := range []string{"up", "left", "up", "left"} {
		runE2EJSON(t, env, "scroll", direction, "10000", "--tab", tab, "--json")
		runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	}
	check("stable origin", `(() => {`+nestedUnchanged+`
      return scrollX === 0 && scrollY === 0 && nestedStable;
    })()`)
}

func TestE2ECLISnapshotModes(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("snapshot modes open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	textOnly := runE2EJSON(t, env, "snapshot", "--text-only", "--json").Data.SnapshotData
	if textOnly == nil {
		t.Fatal("text-only snapshot returned no snapshot data")
	}
	requireContains(t, textOnly.Snapshot, "# E2E Verify Home", "text-only snapshot")
	requireContains(t, textOnly.Snapshot, site.URL()+"/", "text-only snapshot")
	requireContains(t, textOnly.Snapshot, "E2E Verify Site", "text-only snapshot")
	requireNotContains(t, textOnly.Snapshot, "[ref=", "text-only snapshot")
	if len(textOnly.Elements) != 0 || len(textOnly.Refs) != 0 {
		t.Fatalf("text-only snapshot unexpectedly returned refs: %+v", textOnly)
	}

	interactive := runE2EJSON(t, env, "snapshot", "--interactive", "--json").Data.SnapshotData
	if interactive == nil {
		t.Fatal("interactive snapshot returned no snapshot data")
	}
	requireContains(t, interactive.Snapshot, "Click counter", "interactive snapshot")
	requireContains(t, interactive.Snapshot, "E2E text input", "interactive snapshot")
	requireNotContains(t, interactive.Snapshot, "not clicked", "interactive snapshot")
	clickRef := refByName(t, interactive, "Click counter")
	preservedTextOnly := runE2EJSON(t, env, "snapshot", "--text-only", "--json").Data.SnapshotData
	if preservedTextOnly == nil || len(preservedTextOnly.Refs) != 0 {
		t.Fatalf("follow-up text-only snapshot unexpectedly returned refs: %+v", preservedTextOnly)
	}
	runE2EJSON(t, env, "click", clickRef, "--tab", tab, "--json")
	requireEvalString(t, env, `document.querySelector("#clicked-result").textContent`, "clicked 1")

	compactDepth := runE2EJSON(t, env, "snapshot", "--compact", "--depth", "1", "--json").Data.SnapshotData
	if compactDepth == nil {
		t.Fatal("compact/depth snapshot returned no snapshot data")
	}
	requireContains(t, compactDepth.Snapshot, "Click counter", "compact/depth snapshot")
	requireNotContains(t, compactDepth.Snapshot, "<button>", "compact/depth snapshot")
	requireNotContains(t, compactDepth.Snapshot, `text "Click me"`, "compact/depth snapshot")

	selected := runE2EJSON(t, env, "snapshot", "--selector", "click-button", "--json").Data.SnapshotData
	if selected == nil {
		t.Fatal("selector-filtered snapshot returned no snapshot data")
	}
	requireContains(t, selected.Snapshot, "Click counter", "selector-filtered snapshot")
	requireNotContains(t, selected.Snapshot, "Hover target", "selector-filtered snapshot")
	if len(selected.Elements) != 1 || selected.Elements[0].Name != "Click counter" {
		t.Fatalf("selector-filtered elements = %+v", selected.Elements)
	}

	buttons := runE2EJSON(t, env, "snapshot", "--role", "button", "--json").Data.SnapshotData
	if buttons == nil {
		t.Fatal("role-filtered snapshot returned no snapshot data")
	}
	requireContains(t, buttons.Snapshot, "Click counter", "role-filtered snapshot")
	requireContains(t, buttons.Snapshot, "Hover target", "role-filtered snapshot")
	requireNotContains(t, buttons.Snapshot, "E2E text input", "role-filtered snapshot")
	for _, el := range buttons.Elements {
		if el.Role != "button" {
			t.Fatalf("role-filtered snapshot returned non-button element: %+v", el)
		}
	}
}
