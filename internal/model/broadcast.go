package model

// BroadcastFont represents an uploaded custom font.
type BroadcastFont struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Filename     string `json:"filename"`
	OriginalName string `json:"original_name"`
	Format       string `json:"format"` // ttf, woff, woff2
	UploadedAt   string `json:"uploaded_at"`
}

// BroadcastPage represents a single page within a broadcast mode.
type BroadcastPage struct {
	ID         int64            `json:"id"`
	Mode       string            `json:"mode"` // before, contesting, after
	Title      string            `json:"title"`
	SortOrder  int               `json:"sort_order"`
	DurationMs int               `json:"duration_ms"` // 0 = manual advance
	BgColor    string            `json:"bg_color"`
	Transition string            `json:"transition"`
	Items      []BroadcastItem   `json:"items,omitempty"`
}

// BroadcastItem represents a draggable element on a broadcast page.
type BroadcastItem struct {
	ID           int64   `json:"id"`
	PageID       int64   `json:"page_id"`
	ItemType     string  `json:"item_type"` // text, image, countdown, scrolling_notice, alert
	Content      string  `json:"content"`
	PosX         float64 `json:"pos_x"`
	PosY         float64 `json:"pos_y"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	FontSize     string  `json:"font_size"`
	FontColor    string  `json:"font_color"`
	FontWeight   string  `json:"font_weight"`
	TextAlign    string  `json:"text_align"`
	BgColor      string  `json:"bg_color"`
	BorderRadius string  `json:"border_radius"`
	Animation    string  `json:"animation"`
	ZIndex       int     `json:"z_index"`
	ExtraJSON    string  `json:"extra_json"`
}

// BroadcastConfig holds global broadcast settings.
type BroadcastConfig struct {
	ActiveFont      string `json:"active_font"`
	CountdownTarget string `json:"countdown_target"`
	BaseURL         string `json:"base_url"`
	ReferenceWidth  string `json:"reference_width"`
	SyncReset       string `json:"sync_reset,omitempty"` // mode name to reset started_at
	PushedState     string `json:"pushed_state"`
}
