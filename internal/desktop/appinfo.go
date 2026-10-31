package desktop

// AppInfo is display data read from the OS, never part of a saved rule.
// Icon is a PNG data URL; an empty value asks the UI to show a placeholder.
type AppInfo struct {
	Name string `json:"name"`
	Icon string `json:"icon,omitempty"`
}
