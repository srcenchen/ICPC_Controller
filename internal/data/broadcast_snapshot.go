package data

import (
	"fmt"
	"strconv"
	"time"

	"ICPCRemoteControl/internal/model"
)

type BroadcastSnapshot struct {
	Source   string                `json:"source"`
	Revision int64                 `json:"revision"`
	Pages    []model.BroadcastPage `json:"pages"`
	Fonts    []model.BroadcastFont `json:"fonts"`
	Config   map[string]string     `json:"config"`
}

var broadcastSharedKeys = []string{"active_font", "countdown_target", "reference_width"}

func (repo *BroadcastRepo) Snapshot(source string) (*BroadcastSnapshot, error) {
	transaction, err := repo.db.Begin()
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback()
	var revision int64
	err = transaction.QueryRow(`INSERT INTO broadcast_config(key,value) VALUES('cloud_publish_revision','1')
		ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1 RETURNING value`).Scan(&revision)
	if err != nil {
		return nil, err
	}
	reader := &BroadcastRepo{reader: transaction}
	snapshot := &BroadcastSnapshot{Source: source, Revision: revision, Config: make(map[string]string), Pages: []model.BroadcastPage{}}
	for _, mode := range []string{"before", "contesting", "after"} {
		pages, err := reader.GetPagesWithItems(mode)
		if err != nil {
			return nil, err
		}
		snapshot.Pages = append(snapshot.Pages, pages...)
	}
	snapshot.Fonts, err = reader.ListFonts()
	if err != nil {
		return nil, err
	}
	for _, key := range broadcastSharedKeys {
		snapshot.Config[key], err = reader.GetConfig(key)
		if err != nil {
			return nil, err
		}
	}
	return snapshot, transaction.Commit()
}

func (repo *BroadcastRepo) ApplySnapshot(snapshot *BroadcastSnapshot, action, mode string) (bool, error) {
	transaction, err := repo.db.Begin()
	if err != nil {
		return false, err
	}
	defer transaction.Rollback()
	key := "cloud_applied_revision_" + snapshot.Source
	result, err := transaction.Exec(`INSERT INTO broadcast_config(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value WHERE CAST(broadcast_config.value AS INTEGER)<CAST(excluded.value AS INTEGER)`, key, strconv.FormatInt(snapshot.Revision, 10))
	if err != nil {
		return false, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return false, nil
	}
	if action == "sync" || action == "start" {
		for _, table := range []string{"broadcast_items", "broadcast_pages", "broadcast_fonts"} {
			if _, err := transaction.Exec("DELETE FROM " + table); err != nil {
				return false, err
			}
		}
		for _, page := range snapshot.Pages {
			if _, err := transaction.Exec(`INSERT INTO broadcast_pages(id,mode,title,sort_order,duration_ms,bg_color,transition) VALUES(?,?,?,?,?,?,?)`, page.ID, page.Mode, page.Title, page.SortOrder, page.DurationMs, page.BgColor, page.Transition); err != nil {
				return false, err
			}
			for _, item := range page.Items {
				if _, err := transaction.Exec(`INSERT INTO broadcast_items(id,page_id,item_type,content,pos_x,pos_y,width,height,font_size,font_color,font_weight,text_align,bg_color,border_radius,animation,z_index,extra_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, page.ID, item.ItemType, item.Content, item.PosX, item.PosY, item.Width, item.Height, item.FontSize, item.FontColor, item.FontWeight, item.TextAlign, item.BgColor, item.BorderRadius, item.Animation, item.ZIndex, item.ExtraJSON); err != nil {
					return false, err
				}
			}
		}
		for _, font := range snapshot.Fonts {
			if _, err := transaction.Exec(`INSERT INTO broadcast_fonts(id,name,filename,original_name,format,uploaded_at) VALUES(?,?,?,?,?,?)`, font.ID, font.Name, font.Filename, font.OriginalName, font.Format, font.UploadedAt); err != nil {
				return false, err
			}
		}
		for _, configKey := range broadcastSharedKeys {
			if _, err := transaction.Exec(`INSERT INTO broadcast_config(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, configKey, snapshot.Config[configKey]); err != nil {
				return false, err
			}
		}
	}
	updates := make(map[string]string)
	switch action {
	case "start":
		updates["pushed_state"] = mode
		updates["broadcast_started_at_"+mode] = time.Now().Format(time.RFC3339Nano)
	case "stop":
		updates["pushed_state"] = ""
	case "reset":
		updates["broadcast_started_at_"+mode] = time.Now().Format(time.RFC3339Nano)
	case "sync":
	default:
		return false, fmt.Errorf("invalid broadcast action")
	}
	for configKey, value := range updates {
		if _, err := transaction.Exec(`INSERT INTO broadcast_config(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, configKey, value); err != nil {
			return false, err
		}
	}
	return true, transaction.Commit()
}
