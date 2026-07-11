//go:build !darwin

package daemon

// CDP modifier mask: ctrl|shift. Chromium on Linux and Windows uses
// Ctrl+Shift+V for paste-as-plain-text.
const pasteModifierMask = 2 | 8
