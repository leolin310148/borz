package daemon

// CDP modifier mask: meta|shift. Chromium on macOS uses Command+Shift+V for
// paste-as-plain-text.
const pasteModifierMask = 4 | 8
