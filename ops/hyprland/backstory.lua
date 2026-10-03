-- Hyprland windowrule for the Backstory panel (tasks 93f7c6fd, 2606210b), in
-- Omarchy's Lua config format.
-- The panel is a regular floating toplevel (Quickshell FloatingWindow), not a
-- layer-shell surface, so Hyprland's own move/resize binds apply. Every
-- Quickshell FloatingWindow has class org.quickshell, so the rule matches BOTH
-- class and title: title `backstory` is Panel.qml's `windowTitle`;
-- ops/hyprland_windowrule_test.go keeps the two in sync.
-- Install: copy to ~/.config/hypr/ and load it from hyprland.lua as module hypr.backstory.
-- Size 380x640 matches Panel.qml's card (a Hyprland size rule overrides QML size).
-- `o` is Omarchy's global (default/hypr/helpers.lua defines it); no require.

local BACKSTORY = { class = "^(org\\.quickshell)$", title = "^(backstory)$" }

o.window(BACKSTORY, {
  float = true,
  size = { 380, 640 },
  move = { "(monitor_w-window_w-20)", "(monitor_h*0.04)" },
})
