package config

import (
	"fmt"
	"regexp"
	"slices"
)

// RotationGroup names one alias. The model order behind the alias is produced
// by the ranking engine, not configured by hand.
type RotationGroup struct {
	Alias string `json:"alias"`
}

type RotationConfig struct {
	SOTA               RotationGroup `json:"sota"`
	Sweet              RotationGroup `json:"sweet"`
	FirstOutputSeconds int           `json:"first_output_seconds"`
}

func (c *RotationConfig) Defaults() {
	if c.SOTA.Alias == "" {
		c.SOTA.Alias = "sota"
	}
	if c.Sweet.Alias == "" {
		c.Sweet.Alias = "sweet"
	}
	if c.FirstOutputSeconds == 0 {
		c.FirstOutputSeconds = 30
	}
}

func (c RotationConfig) Validate(realModels []string) error {
	if c.FirstOutputSeconds < 1 || c.FirstOutputSeconds > 300 {
		return fmt.Errorf("首个有效输出超时需为 1–300 秒")
	}
	if c.SOTA.Alias == c.Sweet.Alias {
		return fmt.Errorf("两个分组的别名不能相同")
	}
	valid := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	for _, group := range []RotationGroup{c.SOTA, c.Sweet} {
		if !valid.MatchString(group.Alias) {
			return fmt.Errorf("别名需为 1–64 位英文字母、数字、点、短横线或下划线")
		}
		if slices.Contains(realModels, group.Alias) {
			return fmt.Errorf("别名不能与真实模型 ID 相同")
		}
	}
	return nil
}
