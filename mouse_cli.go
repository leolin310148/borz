package main

import (
	"fmt"
	"math"
	"strconv"

	"github.com/leolin310148/borz/internal/protocol"
)

func mouseCLIRequest(positional, raw []string) (*protocol.Request, error) {
	if len(positional) != 3 {
		return nil, fmt.Errorf("Usage: borz mouse <click|move|down|up> <x> <y> [--button left|right|middle|none]; coordinates are viewport CSS pixels")
	}
	switch positional[0] {
	case "click", "move", "down", "up":
	default:
		return nil, fmt.Errorf("mouse action must be click, move, down, or up")
	}
	x, errX := strconv.ParseFloat(positional[1], 64)
	y, errY := strconv.ParseFloat(positional[2], 64)
	if errX != nil || errY != nil || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) || x < 0 || y < 0 {
		return nil, fmt.Errorf("mouse coordinates must be finite non-negative numbers")
	}
	button := getArgValue(raw, "--button")
	switch button {
	case "", "left", "right", "middle", "none":
	default:
		return nil, fmt.Errorf("mouse button must be left, right, middle, or none")
	}
	modifiers, err := parsePressModifiers(getArgValue(raw, "--modifiers"))
	if err != nil {
		return nil, err
	}
	return &protocol.Request{ID: newID(), Action: protocol.ActionMouse, MouseType: positional[0], X: &x, Y: &y, Button: button, Modifiers: modifiers}, nil
}
