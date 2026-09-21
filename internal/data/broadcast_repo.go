package data

import (
	"database/sql"
	"fmt"
	"time"

	"ICPCRemoteControl/internal/model"
)

// BroadcastRepo handles CRUD for broadcast_pages, broadcast_items, broadcast_fonts, broadcast_config.
type BroadcastRepo struct {
	db     *sql.DB
	reader broadcastReader
}

type broadcastReader interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// NewBroadcastRepo creates a new BroadcastRepo.
func NewBroadcastRepo(db *sql.DB) *BroadcastRepo {
	return &BroadcastRepo{db: db, reader: db}
}

// ---- Config ----

func (r *BroadcastRepo) GetConfig(key string) (string, error) {
	var v string
	err := r.reader.QueryRow(`SELECT value FROM broadcast_config WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (r *BroadcastRepo) SetConfig(key, value string) error {
	_, err := r.db.Exec(`INSERT INTO broadcast_config(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// ---- Fonts ----

func (r *BroadcastRepo) ListFonts() ([]model.BroadcastFont, error) {
	rows, err := r.reader.Query(`SELECT id, name, filename, original_name, format, uploaded_at FROM broadcast_fonts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fonts []model.BroadcastFont
	for rows.Next() {
		var f model.BroadcastFont
		if err := rows.Scan(&f.ID, &f.Name, &f.Filename, &f.OriginalName, &f.Format, &f.UploadedAt); err != nil {
			return nil, err
		}
		fonts = append(fonts, f)
	}
	if fonts == nil {
		fonts = []model.BroadcastFont{}
	}
	return fonts, rows.Err()
}

func (r *BroadcastRepo) CreateFont(f *model.BroadcastFont) error {
	f.UploadedAt = time.Now().Format(time.RFC3339)
	result, err := r.db.Exec(`INSERT INTO broadcast_fonts(name,filename,original_name,format,uploaded_at) VALUES(?,?,?,?,?)`,
		f.Name, f.Filename, f.OriginalName, f.Format, f.UploadedAt)
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	f.ID = id
	return nil
}

func (r *BroadcastRepo) DeleteFont(id int64) error {
	_, err := r.db.Exec(`DELETE FROM broadcast_fonts WHERE id=?`, id)
	return err
}

func (r *BroadcastRepo) GetFontByID(id int64) (*model.BroadcastFont, error) {
	f := &model.BroadcastFont{}
	err := r.db.QueryRow(`SELECT id, name, filename, original_name, format, uploaded_at FROM broadcast_fonts WHERE id=?`, id).
		Scan(&f.ID, &f.Name, &f.Filename, &f.OriginalName, &f.Format, &f.UploadedAt)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// ---- Pages ----

func (r *BroadcastRepo) ListPages(mode string) ([]model.BroadcastPage, error) {
	rows, err := r.reader.Query(`SELECT id, mode, title, sort_order, duration_ms, bg_color, transition FROM broadcast_pages WHERE mode=? ORDER BY sort_order`, mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pages []model.BroadcastPage
	for rows.Next() {
		var p model.BroadcastPage
		if err := rows.Scan(&p.ID, &p.Mode, &p.Title, &p.SortOrder, &p.DurationMs, &p.BgColor, &p.Transition); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	if pages == nil {
		pages = []model.BroadcastPage{}
	}
	return pages, rows.Err()
}

func (r *BroadcastRepo) CreatePage(p *model.BroadcastPage) error {
	result, err := r.db.Exec(`INSERT INTO broadcast_pages(mode,title,sort_order,duration_ms,bg_color,transition) VALUES(?,?,?,?,?,?)`,
		p.Mode, p.Title, p.SortOrder, p.DurationMs, p.BgColor, p.Transition)
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	p.ID = id
	return nil
}

func (r *BroadcastRepo) UpdatePage(p *model.BroadcastPage) error {
	_, err := r.db.Exec(`UPDATE broadcast_pages SET title=?,duration_ms=?,bg_color=?,transition=? WHERE id=?`,
		p.Title, p.DurationMs, p.BgColor, p.Transition, p.ID)
	return err
}

func (r *BroadcastRepo) DuplicatePage(id int64) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO broadcast_pages(mode,title,sort_order,duration_ms,bg_color,transition)
		SELECT mode,title || '（副本）',(SELECT COALESCE(MAX(sort_order),0)+1 FROM broadcast_pages),duration_ms,bg_color,transition FROM broadcast_pages WHERE id=?`, id)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return 0, sql.ErrNoRows
	}
	newID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(`INSERT INTO broadcast_items(page_id,item_type,content,pos_x,pos_y,width,height,font_size,font_color,font_weight,text_align,bg_color,border_radius,animation,z_index,extra_json)
		SELECT ?,item_type,content,pos_x,pos_y,width,height,font_size,font_color,font_weight,text_align,bg_color,border_radius,animation,z_index,extra_json FROM broadcast_items WHERE page_id=?`, newID, id)
	if err != nil {
		return 0, err
	}
	return newID, tx.Commit()
}

func (r *BroadcastRepo) DeletePage(id int64) error {
	// Delete all items on this page first.
	if _, err := r.db.Exec(`DELETE FROM broadcast_items WHERE page_id=?`, id); err != nil {
		return err
	}
	_, err := r.db.Exec(`DELETE FROM broadcast_pages WHERE id=?`, id)
	return err
}

func (r *BroadcastRepo) UpdatePageOrder(pages []model.BroadcastPage) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, p := range pages {
		if _, err := tx.Exec(`UPDATE broadcast_pages SET sort_order=? WHERE id=?`, i, p.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- Items ----

func (r *BroadcastRepo) ListItems(pageID int64) ([]model.BroadcastItem, error) {
	rows, err := r.db.Query(`SELECT id, page_id, item_type, content, pos_x, pos_y, width, height, font_size, font_color, font_weight, text_align, bg_color, border_radius, animation, z_index, extra_json FROM broadcast_items WHERE page_id=? ORDER BY z_index, id`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.BroadcastItem
	for rows.Next() {
		var it model.BroadcastItem
		if err := rows.Scan(&it.ID, &it.PageID, &it.ItemType, &it.Content, &it.PosX, &it.PosY, &it.Width, &it.Height, &it.FontSize, &it.FontColor, &it.FontWeight, &it.TextAlign, &it.BgColor, &it.BorderRadius, &it.Animation, &it.ZIndex, &it.ExtraJSON); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if items == nil {
		items = []model.BroadcastItem{}
	}
	return items, rows.Err()
}

func (r *BroadcastRepo) ListAllItemsByMode(mode string) (map[int64][]model.BroadcastItem, error) {
	rows, err := r.reader.Query(`
		SELECT i.id, i.page_id, i.item_type, i.content, i.pos_x, i.pos_y, i.width, i.height,
		       i.font_size, i.font_color, i.font_weight, i.text_align, i.bg_color, i.border_radius,
		       i.animation, i.z_index, i.extra_json
		FROM broadcast_items i
		JOIN broadcast_pages p ON p.id = i.page_id
		WHERE p.mode = ?
		ORDER BY i.page_id, i.z_index, i.id`, mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64][]model.BroadcastItem)
	for rows.Next() {
		var it model.BroadcastItem
		if err := rows.Scan(&it.ID, &it.PageID, &it.ItemType, &it.Content, &it.PosX, &it.PosY, &it.Width, &it.Height, &it.FontSize, &it.FontColor, &it.FontWeight, &it.TextAlign, &it.BgColor, &it.BorderRadius, &it.Animation, &it.ZIndex, &it.ExtraJSON); err != nil {
			return nil, err
		}
		result[it.PageID] = append(result[it.PageID], it)
	}
	return result, rows.Err()
}

func (r *BroadcastRepo) CreateItem(it *model.BroadcastItem) error {
	result, err := r.db.Exec(`INSERT INTO broadcast_items(page_id,item_type,content,pos_x,pos_y,width,height,font_size,font_color,font_weight,text_align,bg_color,border_radius,animation,z_index,extra_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.PageID, it.ItemType, it.Content, it.PosX, it.PosY, it.Width, it.Height, it.FontSize, it.FontColor, it.FontWeight, it.TextAlign, it.BgColor, it.BorderRadius, it.Animation, it.ZIndex, it.ExtraJSON)
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	it.ID = id
	return nil
}

func (r *BroadcastRepo) UpdateItem(it *model.BroadcastItem) error {
	// Partial update: only update presentation fields, never page_id or item_type.
	_, err := r.db.Exec(`UPDATE broadcast_items SET content=?,pos_x=?,pos_y=?,width=?,height=?,font_size=?,font_color=?,font_weight=?,text_align=?,bg_color=?,border_radius=?,animation=?,z_index=?,extra_json=? WHERE id=?`,
		it.Content, it.PosX, it.PosY, it.Width, it.Height, it.FontSize, it.FontColor, it.FontWeight, it.TextAlign, it.BgColor, it.BorderRadius, it.Animation, it.ZIndex, it.ExtraJSON, it.ID)
	return err
}

func (r *BroadcastRepo) UpdateItemPosition(id int64, posX, posY float64) error {
	_, err := r.db.Exec(`UPDATE broadcast_items SET pos_x=?, pos_y=? WHERE id=?`, posX, posY, id)
	return err
}

func (r *BroadcastRepo) UpdateItemPositionAndSize(id int64, posX, posY, width, height float64) error {
	_, err := r.db.Exec(`UPDATE broadcast_items SET pos_x=?, pos_y=?, width=?, height=? WHERE id=?`, posX, posY, width, height, id)
	return err
}

func (r *BroadcastRepo) GetItemByID(id int64) (*model.BroadcastItem, error) {
	it := &model.BroadcastItem{}
	err := r.db.QueryRow(`SELECT id, page_id, item_type, content, pos_x, pos_y, width, height, font_size, font_color, font_weight, text_align, bg_color, border_radius, animation, z_index, extra_json FROM broadcast_items WHERE id=?`, id).
		Scan(&it.ID, &it.PageID, &it.ItemType, &it.Content, &it.PosX, &it.PosY, &it.Width, &it.Height, &it.FontSize, &it.FontColor, &it.FontWeight, &it.TextAlign, &it.BgColor, &it.BorderRadius, &it.Animation, &it.ZIndex, &it.ExtraJSON)
	if err != nil {
		return nil, err
	}
	return it, nil
}

func (r *BroadcastRepo) DeleteItem(id int64) error {
	_, err := r.db.Exec(`DELETE FROM broadcast_items WHERE id=?`, id)
	return err
}

// ---- Countdown ----

func (r *BroadcastRepo) GetCountdownTarget() (target string, serverNow string, err error) {
	target, err = r.GetConfig("countdown_target")
	if err != nil {
		return "", "", err
	}
	return target, time.Now().Format(time.RFC3339), nil
}

// ---- Helpers ----

// GetPagesWithItems returns all pages for a mode with their items populated.
func (r *BroadcastRepo) GetPagesWithItems(mode string) ([]model.BroadcastPage, error) {
	pages, err := r.ListPages(mode)
	if err != nil {
		return nil, fmt.Errorf("list pages: %w", err)
	}
	itemsByPage, err := r.ListAllItemsByMode(mode)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	for i := range pages {
		if items, ok := itemsByPage[pages[i].ID]; ok {
			pages[i].Items = items
		} else {
			pages[i].Items = []model.BroadcastItem{}
		}
	}
	return pages, nil
}
