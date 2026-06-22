module search-engine

go 1.24.4

require github.com/Saurabh-1785/gocrawl v0.0.0

require golang.org/x/net v0.40.0 // indirect

// Use local gocrawl during development.
// Remove this line and set a proper version when publishing.
replace github.com/Saurabh-1785/gocrawl => ../gocrawl
