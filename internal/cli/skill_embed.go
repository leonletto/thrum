package cli

import "embed"

//go:embed skill/thrum/SKILL.md
//go:embed skill/thrum/references/*
var SkillFS embed.FS
