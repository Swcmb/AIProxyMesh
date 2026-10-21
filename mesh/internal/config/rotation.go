package config

import (
	"fmt"
	"regexp"
	"slices"
)

<<<<<<< ours
// RotationGroup names one alias. The model order behind the alias is produced
// by the ranking engine, not configured by hand.
=======
// SOTAModels lists the models of the "sota" rotation group in their default
// order. That group must hold exactly these models; every other model belongs
// to the "sweet" group.
var SOTAModels = []string{"deepseek-v4.1-flash", "glm-5.3", "grok-4.6", "kimi-k3", "qwen3.8-max"}

// RotationGroup is one model-rotation group: Alias is the model name clients
// request to use the group, and Order is the failover order of its models.
>>>>>>> theirs
type RotationGroup struct {
	Alias string `json:"alias"`
}

<<<<<<< ours
=======
// RotationConfig configures the two model-rotation groups, which are served by
// the Go tier. FirstOutputSeconds bounds how long an attempt may take to
// produce its first output before the next attempt runs.
>>>>>>> theirs
type RotationConfig struct {
	SOTA               RotationGroup `json:"sota"`
	Sweet              RotationGroup `json:"sweet"`
	FirstOutputSeconds int           `json:"first_output_seconds"`
}

<<<<<<< ours
=======
// IsSOTA reports whether model is one of SOTAModels.
func IsSOTA(model string) bool { return slices.Contains(SOTAModels, model) }

// Defaults fills in what is unset: the aliases "sota" and "sweet", the SOTA
// order (SOTAModels) and a 30-second first-output timeout. The sweet order has
// no default: the rotation state adds the Go tier's other models to the group
// as they appear.
>>>>>>> theirs
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

<<<<<<< ours
=======
// Validate requires a first-output timeout of 1-300 seconds; two distinct
// aliases of up to 64 letters, digits, dots, dashes or underscores, starting
// with a letter or digit, that do not shadow a model ID (one of realModels,
// SOTAModels or the sweet order); and orders without empty or repeated
// entries, where the SOTA group holds exactly SOTAModels and the sweet group
// only other models. A nil realModels skips the catalog check.
>>>>>>> theirs
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
