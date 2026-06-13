module github.com/gollem-dev/tools/slack

go 1.26.4

require (
	github.com/gollem-dev/gollem v0.26.0
	github.com/gollem-dev/tools/internal v0.0.0
	github.com/m-mizutani/goerr/v2 v2.0.1
	github.com/m-mizutani/gt v0.2.1
)

require (
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
)

// Dev-time local resolution of the shared internal module. Replace with a real
// version (tag internal/vX) before publishing.
replace github.com/gollem-dev/tools/internal => ../internal
