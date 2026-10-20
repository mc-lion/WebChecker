package webassets

import "embed"

//go:embed templates/*.html static/css/* static/js/* static/vendor/gravity/*
var FS embed.FS
