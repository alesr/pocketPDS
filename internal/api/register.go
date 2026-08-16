package api

import (
	_ "github.com/bluesky-social/indigo/api/atproto"
	_ "github.com/bluesky-social/indigo/api/bsky"
	_ "github.com/bluesky-social/indigo/api/chat"
)

// Blank imports register the generated lexicon record types with lexutil's
// global registry so that lexutil.JsonDecodeValue (used by the generated
// createRecord/putRecord input types) can decode app.bsky.*, chat.bsky.* and
// com.atproto.* records on the write path.
