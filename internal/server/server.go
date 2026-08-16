package server

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/alesr/pocketPDS/internal/admin"
	"github.com/alesr/pocketPDS/internal/api"
	"github.com/alesr/pocketPDS/internal/blob"
	"github.com/alesr/pocketPDS/internal/bridge"
	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/email"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/tunnel"
)

type Server struct {
	cfg   *config.Config
	store *db.Store
	mux   *http.ServeMux
}

func New(cfg *config.Config, store *db.Store, tunnels *tunnel.Manager) (*Server, error) {
	mgr := repo.NewManager(store)
	mgr.SetPublicURL(cfg.PublicURL)
	blobs, err := blob.New(filepath.Join(cfg.DataDir, "blobs"), store)
	if err != nil {
		return nil, err
	}
	mgr.SetBlobStore(blobs)
	sender := email.New(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
	bridgeSvc := bridge.New(cfg, store, mgr, blobs)

	s := &Server{cfg: cfg, store: store, mux: http.NewServeMux()}

	authLimiter := newRateLimiter(5, 10)
	clientKey := clientIP(cfg.TrustProxy)

	s.mux.HandleFunc("GET /xrpc/_health", api.HandleHealth(store))
	s.mux.HandleFunc("GET /xrpc/com.atproto.server.describeServer", api.HandleDescribeServer(cfg))
	s.mux.HandleFunc("GET /xrpc/com.atproto.identity.resolveHandle", api.HandleResolveHandle)
	s.mux.HandleFunc("GET /xrpc/com.atproto.identity.resolveDid", api.HandleResolveDid)
	s.mux.HandleFunc("POST /xrpc/com.atproto.identity.updateHandle", api.HandleUpdateHandle(cfg, store, mgr))

	// did:web document hosting
	s.mux.HandleFunc("GET /.well-known/did.json", api.HandleDidWebWellKnown(store))
	s.mux.HandleFunc("GET /.well-known/atproto-did", api.HandleAtprotoDid(store))
	s.mux.HandleFunc("GET /{handle}/did.json", api.HandleDidWebPath(store))

	s.mux.HandleFunc("POST /xrpc/com.atproto.server.createSession", rateLimit(authLimiter, clientKey, api.HandleCreateSession(store)))
	s.mux.HandleFunc("GET /xrpc/com.atproto.server.getSession", api.HandleGetSession(store))
	s.mux.HandleFunc("POST /xrpc/com.atproto.server.refreshSession", rateLimit(authLimiter, clientKey, api.HandleRefreshSession(store)))
	s.mux.HandleFunc("POST /xrpc/com.atproto.server.deleteSession", api.HandleDeleteSession(store))
	s.mux.HandleFunc("POST /xrpc/com.atproto.server.createAccount", rateLimit(authLimiter, clientKey, api.HandleCreateAccount(cfg, store, mgr)))
	s.mux.HandleFunc("POST /xrpc/com.atproto.server.deactivateAccount", api.HandleDeactivateAccount(store, mgr))
	s.mux.HandleFunc("POST /xrpc/com.atproto.server.activateAccount", api.HandleActivateAccount(store, mgr))
	s.mux.HandleFunc("POST /xrpc/com.atproto.server.deleteAccount", api.HandleDeleteAccount(store, mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.server.checkAccountStatus", api.HandleCheckAccountStatus(store))

	s.mux.HandleFunc("POST /xrpc/com.atproto.server.createAppPassword", api.HandleCreateAppPassword(store))
	s.mux.HandleFunc("GET /xrpc/com.atproto.server.listAppPasswords", api.HandleListAppPasswords(store))
	s.mux.HandleFunc("POST /xrpc/com.atproto.server.revokeAppPassword", api.HandleRevokeAppPassword(store))

	s.mux.HandleFunc("POST /xrpc/com.atproto.server.createInviteCodes", api.HandleCreateInviteCodes(store))
	s.mux.HandleFunc("GET /xrpc/com.atproto.server.getAccountInviteCodes", api.HandleGetAccountInviteCodes(store))

	s.mux.HandleFunc("POST /xrpc/com.atproto.server.requestEmailConfirmation", api.HandleRequestEmailConfirmation(store, sender))
	s.mux.HandleFunc("POST /xrpc/com.atproto.server.confirmEmail", api.HandleConfirmEmail(store))
	s.mux.HandleFunc("POST /xrpc/com.atproto.server.requestPasswordReset", api.HandleRequestPasswordReset(store, sender))
	s.mux.HandleFunc("POST /xrpc/com.atproto.server.resetPassword", api.HandleResetPassword(store))

	s.mux.HandleFunc("POST /xrpc/com.atproto.repo.createRecord", api.HandleCreateRecord(store, mgr))
	s.mux.HandleFunc("POST /xrpc/com.atproto.repo.putRecord", api.HandlePutRecord(store, mgr))
	s.mux.HandleFunc("POST /xrpc/com.atproto.repo.deleteRecord", api.HandleDeleteRecord(store, mgr))
	s.mux.HandleFunc("POST /xrpc/com.atproto.repo.applyWrites", api.HandleApplyWrites(store, mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.repo.getRecord", api.HandleGetRecord(store, mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.repo.listRecords", api.HandleListRecords(store, mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.repo.describeRepo", api.HandleDescribeRepo(store, mgr))

	s.mux.HandleFunc("POST /xrpc/com.atproto.repo.uploadBlob", api.HandleUploadBlob(store, blobs, cfg.BlobSizeLimit))

	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.getLatestCommit", api.HandleGetLatestCommit(mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.getRepo", api.HandleGetRepo(mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.getCheckout", api.HandleGetCheckout(mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.getRecord", api.HandleSyncGetRecord(mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.getBlocks", api.HandleGetBlocks(mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.getBlob", api.HandleGetBlob(blobs))
	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.listRepos", api.HandleListRepos(mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.listBlobs", api.HandleListBlobs(blobs))
	s.mux.HandleFunc("POST /xrpc/com.atproto.sync.notifyOfUpdate", api.HandleNotifyOfUpdate(store))
	s.mux.HandleFunc("POST /xrpc/com.atproto.sync.requestCrawl", api.HandleRequestCrawl)
	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.getHostStatus", api.HandleGetHostStatus(store))
	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.getRepoStatus", api.HandleGetRepoStatus(store, mgr))
	s.mux.HandleFunc("GET /xrpc/com.atproto.sync.subscribeRepos", api.HandleSubscribeRepos(mgr))

	// Minimal single-user AppView (app.bsky.*) so ATProto clients can read
	// this account's own profile, preferences, and feed, plus proxied network
	// reads (search, remote profiles/feeds/threads) via the public AppView.
	s.mux.HandleFunc("GET /xrpc/app.bsky.actor.getProfile", api.HandleAppBskyGetProfile(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.actor.getProfiles", api.HandleAppBskyGetProfiles(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.actor.getPreferences", api.HandleAppBskyGetPreferences(cfg, store, mgr))
	s.mux.HandleFunc("POST /xrpc/app.bsky.actor.putPreferences", api.HandleAppBskyPutPreferences(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.getTimeline", api.HandleAppBskyGetTimeline(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.getAuthorFeed", api.HandleAppBskyGetAuthorFeed(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.getPostThread", api.HandleAppBskyGetPostThread(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.getPosts", api.HandleAppBskyGetPosts(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.getLikes", api.HandleAppBskyGetLikes(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.getRepostedBy", api.HandleAppBskyGetRepostedBy(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.graph.getFollows", api.HandleAppBskyGetFollows(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.graph.getFollowers", api.HandleAppBskyGetFollowers(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.graph.getBlocks", api.HandleAppBskyGetBlocks(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.graph.getMutes", api.HandleAppBskyGetMutes(cfg, store, mgr))
	s.mux.HandleFunc("POST /xrpc/app.bsky.graph.muteActor", api.HandleAppBskyMuteActor(cfg, store, mgr))
	s.mux.HandleFunc("POST /xrpc/app.bsky.graph.unmuteActor", api.HandleAppBskyUnmuteActor(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.graph.getLists", api.HandleAppBskyGetLists(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.graph.getListBlocks", api.HandleAppBskyGetListBlocks(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.graph.getListMutes", api.HandleAppBskyGetListMutes(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.getFeedGenerators", api.HandleAppBskyGetFeedGenerators(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.actor.searchActorsTypeahead", api.HandleAppBskySearchActorsTypeahead(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.notification.listNotifications", api.HandleAppBskyListNotifications(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.actor.searchActors", api.HandleAppBskySearchActors(cfg, store))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.searchPosts", api.HandleAppBskySearchPosts(cfg, store))
	s.mux.HandleFunc("GET /xrpc/app.bsky.actor.getSuggestions", api.HandleAppBskyGetSuggestions(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.getFeed", api.HandleAppBskyGetFeed(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.getFeedGenerator", api.HandleAppBskyGetFeedGenerator(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.feed.getSuggestedFeeds", api.HandleAppBskyGetSuggestedFeeds(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.unspecced.getPopularFeedGenerators", api.HandleAppBskyGetPopularFeedGenerators(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.graph.getSuggestedFollowsByActor", api.HandleAppBskyGetSuggestedFollowsByActor(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.labeler.getServices", api.HandleAppBskyLabelerGetServices(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/app.bsky.notification.getUnreadCount", api.HandleAppBskyGetUnreadCount(cfg, store, mgr))
	s.mux.HandleFunc("POST /xrpc/app.bsky.notification.updateSeen", api.HandleAppBskyUpdateSeen(cfg, store, mgr))
	s.mux.HandleFunc("POST /xrpc/app.bsky.notification.registerPush", api.HandleAppBskyRegisterPush(cfg, store, mgr))
	s.mux.HandleFunc("GET /xrpc/chat.bsky.convo.listConvos", api.HandleChatListConvos(cfg, store, mgr))

	admin.New(cfg, store, mgr, tunnels, bridgeSvc).Register(s.mux)

	return s, nil
}

func (s *Server) Handler() http.Handler { return cors(logUnhandled(s.mux)) }

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logUnhandled records and logs requests to XRPC routes that have no handler,
// so missing endpoints surface in the logs without a mux pattern conflict.
func logUnhandled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusNotFound && strings.HasPrefix(r.URL.Path, "/xrpc/") {
			slog.Warn("unhandled xrpc route", "method", r.Method, "path", r.URL.Path, "query", r.URL.RawQuery)
		}
	})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
