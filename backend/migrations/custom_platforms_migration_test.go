package migrations

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomPlatformsMigration(t *testing.T) {
	content, err := FS.ReadFile("259_custom_platforms.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS custom_platforms (")
	// 主键与全部业务列
	for _, col := range []string{
		"id VARCHAR(32) PRIMARY KEY",
		"display_name VARCHAR(64) NOT NULL",
		"enabled BOOLEAN NOT NULL DEFAULT TRUE",
		"sort_order INTEGER NOT NULL DEFAULT 0",
		"icon_type VARCHAR(16) NOT NULL DEFAULT 'letter'",
		"icon_value TEXT NOT NULL DEFAULT ''",
		"color_preset VARCHAR(24) NOT NULL DEFAULT 'blue'",
		"litellm_provider VARCHAR(64) NOT NULL DEFAULT ''",
		"default_test_model VARCHAR(128) NOT NULL DEFAULT ''",
		"supports_embeddings BOOLEAN NOT NULL DEFAULT FALSE",
		"supports_images BOOLEAN NOT NULL DEFAULT FALSE",
		"profile_json JSONB NOT NULL DEFAULT '{}'::jsonb",
		"created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
		"updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
	} {
		require.Contains(t, sql, col)
	}

	// 排序索引
	require.Contains(t, sql,
		"CREATE INDEX IF NOT EXISTS idx_custom_platforms_sort ON custom_platforms (sort_order, id);")

	// 修正后精简方案不保留软删除列
	require.NotContains(t, sql, "deleted_at")

	// 平台清单由应用层校验，数据库不新增任何 CHECK 约束
	require.NotContains(t, sql, "ADD CONSTRAINT")
	require.NotContains(t, sql, "CHECK (")
}

// 259 之后的迁移不得恢复 242 移除的平台白名单 CHECK 约束。
func TestCustomPlatformsMigrationKeepsNoPlatformCheckConstraints(t *testing.T) {
	const introducedAt = "259_custom_platforms.sql"
	entries, err := fs.ReadDir(FS, ".")
	require.NoError(t, err)
	comments := regexp.MustCompile(`(?ms)/\*.*?\*/|--[^\n]*`)
	addConstraint := regexp.MustCompile(`(?i)\bADD\s+CONSTRAINT\s+"?(user_platform_quotas_platform_check|composite_model_routes_target_platform_check)\b`)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() < introducedAt {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			content, err := FS.ReadFile(entry.Name())
			require.NoError(t, err)
			sql := comments.ReplaceAll(content, nil)
			require.Nil(t, addConstraint.Find(sql))
		})
	}
}
