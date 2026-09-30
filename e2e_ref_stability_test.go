package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
)

// refStabilityFixture reproduces the Salesforce Lightning / Office ribbon
// pattern behind most "ref is stale immediately after snapshot" reports:
// interactive controls live inside open shadow roots, and background widgets
// keep inserting and removing siblings, so any positional XPath is wrong by
// the time the next CLI command runs even though the snapshotted element
// itself is still attached and visible.
const refStabilityFixture = `<!doctype html>
<meta charset="utf-8">
<title>Ref stability fixture</title>
<style>body { font-family: sans-serif; margin: 24px; } button, a, input { font-size: 16px; }</style>
<x-tabs id="tabs"></x-tabs>
<section id="list-a"><h2>Accounts</h2><div class="toolbar"><button class="new">New</button></div></section>
<section id="list-b"><h2>Contacts</h2><div class="toolbar"><button class="new">New</button></div></section>
<x-form id="form"></x-form>
<output id="result">idle</output>
<script>
  const result = document.querySelector('#result');
  customElements.define('x-tabs', class extends HTMLElement {
    connectedCallback() {
      const root = this.attachShadow({ mode: 'open' });
      root.innerHTML = '<ul role="tablist"><li><a role="tab" href="#" data-tab="Home">Home</a></li>' +
        '<li><a role="tab" href="#" data-tab="Parts">Parts</a></li>' +
        '<li><a role="tab" href="#" data-tab="Files">Files</a></li></ul>';
      root.addEventListener('click', event => {
        const tab = event.target.closest('a[role=tab]');
        if (!tab) return;
        event.preventDefault();
        result.textContent = 'tab ' + tab.dataset.tab;
      });
    }
  });
  customElements.define('x-form', class extends HTMLElement {
    connectedCallback() {
      const root = this.attachShadow({ mode: 'open' });
      root.innerHTML = '<div class="row"><label for="acct">Account Name</label><input id="acct"></div>';
      root.querySelector('input').addEventListener('input', event => {
        result.textContent = 'input ' + event.target.value;
      });
    }
  });
  for (const section of document.querySelectorAll('section')) {
    section.querySelector('button.new').addEventListener('click', () => {
      result.textContent = 'new ' + section.querySelector('h2').textContent;
    });
  }
  // Background churn: siblings appear and disappear before every target, in
  // light DOM and inside the shadow roots, shifting positional XPaths.
  let tick = 0;
  setInterval(() => {
    tick++;
    const ul = document.querySelector('#tabs').shadowRoot.querySelector('ul');
    const row = document.querySelector('#form').shadowRoot;
    for (const parent of [ul, row, document.body, ...document.querySelectorAll('.toolbar')]) {
      const spacer = parent.querySelector(':scope > .churn');
      if (spacer) spacer.remove();
      else {
        const node = document.createElement(parent === ul ? 'li' : 'div');
        node.className = 'churn';
        node.textContent = 'widget ' + tick;
        parent.prepend(node);
      }
    }
  }, 40);
</script>`

func TestE2ERefStabilityUnderShadowDOMChurn(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(refStabilityFixture))
	}))
	defer site.Close()

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL, "--new", "--wait-for", "#result", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--json")
	if snapshot.Data.SnapshotData == nil {
		t.Fatalf("snapshot returned no data: %+v", snapshot.Data)
	}
	parts := refByName(t, snapshot.Data.SnapshotData, "Parts")
	files := refByName(t, snapshot.Data.SnapshotData, "Files")
	account := refByRoleName(t, snapshot.Data.SnapshotData, "textbox", "Account Name")
	var newRefs []string
	for ref, info := range snapshot.Data.SnapshotData.Refs {
		if info.Name == "New" {
			newRefs = append(newRefs, ref)
		}
	}
	if len(newRefs) != 2 {
		t.Fatalf("want two New buttons, got %v", newRefs)
	}

	// Let the churn run so every positional XPath has shifted at least once.
	time.Sleep(300 * time.Millisecond)

	runE2EJSON(t, env, "click", parts, "--json")
	requireEvalString(t, env, `document.querySelector("#result").textContent`, "tab Parts")

	time.Sleep(200 * time.Millisecond)
	runE2EJSON(t, env, "click", files, "--json")
	requireEvalString(t, env, `document.querySelector("#result").textContent`, "tab Files")

	// Duplicate accessible names: semantic fallback cannot disambiguate, so
	// the ref must keep pointing at the exact snapshotted node.
	// Contacts comes second in document order, so it has the higher ref.
	contactsNew := newRefs[0]
	if n0, _ := strconv.Atoi(newRefs[0]); true {
		if n1, _ := strconv.Atoi(newRefs[1]); n1 > n0 {
			contactsNew = newRefs[1]
		}
	}
	time.Sleep(200 * time.Millisecond)
	runE2EJSON(t, env, "click", contactsNew, "--json")
	requireEvalString(t, env, `document.querySelector("#result").textContent`, "new Contacts")

	time.Sleep(200 * time.Millisecond)
	runE2EJSON(t, env, "fill", account, "Acme", "--json")
	requireEvalString(t, env, `document.querySelector("#result").textContent`, "input Acme")
}
