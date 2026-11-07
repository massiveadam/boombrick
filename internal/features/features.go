// Package features holds this fork's build-time switches. Each is off unless
// its build tag is given, so the default build is the offline player: local
// music only, nothing fetched from the network, no updates from upstream.
//
//	go build -tags streaming        Spotify, YouTube Music, downloads, imports
//	go build -tags netlookups       covers, lyrics and fonts looked up online
//	go build -tags upstreamupdates  Settings offers upstream Boombrick releases
//
// The code behind them is kept, not deleted, so the fork still merges with
// upstream.
package features
