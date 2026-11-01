-- Hyprland windowrule for the Backstory panel (tasks 93f7c6fd, 2606210b), in
-- Omarchy's Lua config format.
-- The panel is a regular floating toplevel (Quickshell FloatingWindow), not a
-- layer-shell surface, so Hyprland's own move/resize binds apply. Every
-- Quickshell FloatingWindow has class org.quickshell, so the rule matches BOTH
-- class and title: title `backstory` is Panel.qml's `windowTitle`;
-- ops/hyprland_windowrule_test.go keeps the two in sync.
-- Install: `make install` copies this to ~/.config/hypr/backstory.lua; add `require("hypr.backstory")` to hyprland.lua.
-- Size 380x640 matches Panel.qml's card (a Hyprland size rule overrides QML size).
-- `o` is Omarchy's global (default/hypr/helpers.lua defines it); no require.

local BACKSTORY = { class = "^(org\\.quickshell)$", title = "^(backstory)$" }

-- A pop-out from the bar popup opens the window under this title, drawn with no
-- decorations or blur, so it shows nothing until Panel.qml has placed it where
-- the popup's card is; then it takes the title above. Static effects (float,
-- size, move) match the title a window opens with, so this rule floats it.
o.window({ class = "^(org\\.quickshell)$", title = "^(backstory-popout)$" }, {
  float = true,
  no_anim = true,
  decorate = false,
  no_blur = true,
})

o.window(BACKSTORY, {
  float = true,
  size = { 380, 640 },
  move = { "(monitor_w-window_w-20)", "(monitor_h*0.04)" },
  -- No Hyprland window animation, as for Omarchy's own panels
  -- (apps/omarchy-shell.lua); Panel.qml fades the card in itself, as they do.
  no_anim = true,
  -- The theme's shadow (a focus glow) and border, as on any other window.
})

-- Keybind: with the This Week window open, focus it (bringing it to this
-- workspace) or close it when it has focus: Panel.qml's toggleIfOpen, which
-- answers "ok" only when the window is open (the shell's `shell toggle` would
-- hide it without asking). Otherwise open or close the bar popup
-- (BarWidget.qml's IPC target), and with no Backstory widget in the bar, open
-- the window. The command starts with `test`, not `[`: Hyprland's exec reads
-- a leading [ ] as window rules, and the bind then runs nothing.
-- Change BACKSTORY_BIND to taste; `hl` is Hyprland's own Lua global.
local BACKSTORY_BIND = "CTRL + SHIFT + B"
hl.bind(BACKSTORY_BIND, hl.dsp.exec_cmd("test \"$(omarchy-shell backstory.this-week toggleIfOpen 2>/dev/null)\" = ok || omarchy-shell backstory.this-week.popup toggle >/dev/null 2>&1 || omarchy-shell shell summon backstory.this-week '{}'"), { description = "Backstory This Week" })
