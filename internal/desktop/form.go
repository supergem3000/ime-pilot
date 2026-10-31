package desktop

// Form is presentation data. Concrete schemes supply the contents; the desktop
// renderer only understands these controls and never branches on scheme IDs.
type Form struct {
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
}
type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Help     string   `json:"help,omitempty"`
	Required bool     `json:"required,omitempty"`
	Options  []Option `json:"options,omitempty"`
}
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type SchemeInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Platforms   []string `json:"platforms"`
}
