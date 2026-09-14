package cli

import (
	"testing"

	"github.com/spf13/viper"
)

// TestFlagsUnmarshal pins the mapstructure behaviour FlagData depends on: the
// tri-mode lives on root flags shared by every check, and the auto flag's
// presence travels through the synthetic key BindCommandFlags sets.
func TestFlagsUnmarshal(t *testing.T) { //nolint:paralleltest // mutates global viper
	viper.Reset()
	defer viper.Reset()

	viper.Set("repo", "katbyte/prawn")
	viper.Set("max", 200)
	viper.Set("apply-with-ai-auto", 0.85)
	viper.Set("apply-with-ai-auto-given", true)

	f := GetFlags()

	if f.GH.Repo != "katbyte/prawn" {
		t.Errorf("repo: %q — should be katbyte/prawn", f.GH.Repo)
	}
	if f.Modes.Max != 200 {
		t.Errorf("max: modes %d — should be 200", f.Modes.Max)
	}
	// the tri-mode auto flag: threshold from the flag value, presence from the
	// synthetic key BindCommandFlags sets
	if !f.Modes.ApplyWithAIAuto || f.Modes.Threshold != 0.85 {
		t.Errorf("auto mode: given %v threshold %v — want true, 0.85", f.Modes.ApplyWithAIAuto, f.Modes.Threshold)
	}

	owner, name, err := f.RepoOwnerName()
	if err != nil || owner != "katbyte" || name != "prawn" {
		t.Errorf("RepoOwnerName: %q/%q, %v — want katbyte/prawn", owner, name, err)
	}
}
