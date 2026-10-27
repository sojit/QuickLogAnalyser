package web

import "embed"

//go:embed index.html viewer.html styles/* scripts/* fixtures/*
var FS embed.FS
