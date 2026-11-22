package db

import "database/sql"

// telegramChatSettingTables holds per-chat preferences the channel exposes.
var telegramChatSettingTables = []tableDef{
	{
		name: "telegramChatSetting",
		columns: [][2]string{
			{"chatId", "INTEGER PRIMARY KEY"},
			{"autoApprove", "INTEGER NOT NULL DEFAULT 0"},
			{"updatedAt", "TEXT NOT NULL DEFAULT ''"},
		},
	},
}

// EnsureTelegramChatSettingTables creates the per-chat settings table.
func EnsureTelegramChatSettingTables(db *sql.DB) error {
	for _, t := range telegramChatSettingTables {
		if err := ensureTable(db, t); err != nil {
			return err
		}
	}
	return nil
}
