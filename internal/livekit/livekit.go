// Package livekit wraps the LiveKit server SDK: JWT signing, Ingress
// provisioning (WHIP), and live-status polling. The browser viewer JS
// SDK connects to the *public* WS URL; the backend uses the *internal*
// HTTP URL (translated from ws://) for Twirp RPC calls.
package livekit

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type Client struct {
	publicURL   string
	whipBaseURL string
	apiKey      string
	apiSecret   string
	room        *lksdk.RoomServiceClient
	ingress     *lksdk.IngressClient
	logger      *slog.Logger
}

type Config struct {
	URL         string // backend-facing, ws:// or wss://
	PublicURL   string // browser-facing, wss://
	WhipBaseURL string // public WHIP endpoint origin, e.g. https://whip.winton.pro/w
	APIKey      string
	APISecret   string
	Logger      *slog.Logger
}

// New constructs a LiveKit client. Both Room and Ingress Twirp APIs live
// on the LiveKit *server* (port 7880). The ingress *worker* container
// only handles WHIP/RTMP traffic — it doesn't expose a management API.
// Job dispatch from server -> worker happens through Redis behind the
// scenes; the app never talks to the ingress worker directly.
func New(c Config) *Client {
	httpURL := wsToHTTP(c.URL)
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		publicURL:   c.PublicURL,
		whipBaseURL: strings.TrimRight(c.WhipBaseURL, "/"),
		apiKey:      c.APIKey,
		apiSecret:   c.APISecret,
		logger:      logger,
		room:        lksdk.NewRoomServiceClient(httpURL, c.APIKey, c.APISecret),
		ingress:     lksdk.NewIngressClient(httpURL, c.APIKey, c.APISecret),
	}
}

func (c *Client) PublicURL() string { return c.publicURL }

// ─────────────────────── Tokens ───────────────────────

// ViewerOptions configures a viewer JWT. CanChat controls
// CanPublishData (data-channel = chat). Anonymous viewers get
// CanChat=false so the JS UI can hide the input AND a hacked client
// still can't publish.
type ViewerOptions struct {
	Identity    string // unique per session; participant.identity in SDK
	Room        string
	TTL         time.Duration
	CanChat     bool
	DisplayName string // participant.name in SDK
	Metadata    string // arbitrary JSON, participant.metadata in SDK
}

// ViewerToken mints a viewer JWT. Subscribe always-on; data-channel
// (chat) gated by opts.CanChat.
func (c *Client) ViewerToken(opts ViewerOptions) (string, error) {
	at := auth.NewAccessToken(c.apiKey, c.apiSecret)
	canPub, canSub := false, true
	canData := opts.CanChat

	name := opts.DisplayName
	if name == "" {
		name = opts.Identity
	}

	at.SetIdentity(opts.Identity).
		SetName(name).
		AddGrant(&auth.VideoGrant{
			Room:           opts.Room,
			RoomJoin:       true,
			CanPublish:     &canPub,
			CanSubscribe:   &canSub,
			CanPublishData: &canData,
		}).
		SetValidFor(opts.TTL)

	if opts.Metadata != "" {
		at.SetMetadata(opts.Metadata)
	}
	return at.ToJWT()
}

// ─────────────────────── Ingress (WHIP for OBS) ───────────────────────

// IngressCredentials are what the dashboard hands to the streamer.
type IngressCredentials struct {
	IngressID string
	StreamKey string
	WhipURL   string
}

// CreateWHIPIngress provisions a WHIP-input ingress that pushes into the
// user's room (room name = slug, identity = slug). Returns OBS-paste-ready
// credentials.
//
// We construct the WHIP URL from our own WhipBaseURL config rather than
// trusting resp.Url — Ingress only fills resp.Url when its yaml has
// whip_base_url set, which doesn't reliably env-interpolate.
func (c *Client) CreateWHIPIngress(ctx context.Context, slug string) (*IngressCredentials, error) {
	resp, err := c.ingress.CreateIngress(ctx, &livekit.CreateIngressRequest{
		InputType:           livekit.IngressInput_WHIP_INPUT,
		Name:                "winton-" + slug,
		RoomName:            slug,
		ParticipantIdentity: slug,
		ParticipantName:     slug,
	})
	if err != nil {
		return nil, fmt.Errorf("create ingress: %w", err)
	}

	c.logger.Info("ingress created",
		"slug", slug,
		"ingress_id", resp.IngressId,
		"livekit_returned_url", resp.Url,
		"livekit_returned_stream_key_present", resp.StreamKey != "")

	whipURL := c.whipURL(resp.StreamKey)
	if whipURL == "" {
		whipURL = resp.Url // last-resort fallback
	}

	return &IngressCredentials{
		IngressID: resp.IngressId,
		StreamKey: resp.StreamKey,
		WhipURL:   whipURL,
	}, nil
}

// whipURL builds the OBS-paste-ready WHIP URL from our configured public
// base + the per-ingress stream key. LiveKit's WHIP endpoint convention
// is {base}/{stream_key}, with the same stream_key also used as bearer.
func (c *Client) whipURL(streamKey string) string {
	if c.whipBaseURL == "" || streamKey == "" {
		return ""
	}
	return c.whipBaseURL + "/" + streamKey
}

// DeleteIngress is best-effort — "not found" is swallowed.
func (c *Client) DeleteIngress(ctx context.Context, ingressID string) error {
	_, err := c.ingress.DeleteIngress(ctx, &livekit.DeleteIngressRequest{IngressId: ingressID})
	if err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// ─────────────────────── Room status ───────────────────────

// DeleteRoom hard-kicks everyone (publisher + viewers) from a room.
// Used by admin "kick stream" action. Idempotent — no-op if room
// doesn't exist (treated as not-found = already gone).
func (c *Client) DeleteRoom(ctx context.Context, room string) error {
	_, err := c.room.DeleteRoom(ctx, &livekit.DeleteRoomRequest{Room: room})
	if err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// IsLive returns true if `room` has at least one publishing participant.
// Room-not-found is treated as offline (not an error).
func (c *Client) IsLive(ctx context.Context, room string) (bool, error) {
	resp, err := c.room.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: room})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, p := range resp.Participants {
		if len(p.Tracks) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// LiveStream is one currently-publishing channel.
type LiveStream struct {
	Slug        string
	ViewerCount int
	StartedAt   time.Time
}

// ListLive returns every room in the SFU that has a publisher attached.
// Publisher identity == room name == user slug (set when the ingress is
// created). Viewers are recognised by the "guest-" identity prefix.
//
// N+1 query (ListRooms + one ListParticipants per room). Fine while we
// have small concurrent room counts; switch to LiveKit Room.num_publishers
// (newer SDKs expose it) once we have ~50+ concurrent rooms.
func (c *Client) ListLive(ctx context.Context) ([]LiveStream, error) {
	rooms, err := c.room.ListRooms(ctx, &livekit.ListRoomsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list rooms: %w", err)
	}

	out := make([]LiveStream, 0, len(rooms.Rooms))
	for _, room := range rooms.Rooms {
		parts, err := c.room.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: room.Name})
		if err != nil {
			c.logger.Warn("listLive: participants", "room", room.Name, "err", err)
			continue
		}
		hasPublisher := false
		viewers := 0
		for _, p := range parts.Participants {
			// Convention: publisher identity == room name (slug).
			// Everyone else is a viewer regardless of "guest-" prefix.
			if p.Identity == room.Name {
				if len(p.Tracks) > 0 {
					hasPublisher = true
				}
			} else {
				viewers++
			}
		}
		if hasPublisher {
			out = append(out, LiveStream{
				Slug:        room.Name,
				ViewerCount: viewers,
				StartedAt:   time.Unix(room.CreationTime, 0),
			})
		}
	}
	return out, nil
}

// ─────────────────────── helpers ───────────────────────

func wsToHTTP(u string) string {
	u = strings.Replace(u, "wss://", "https://", 1)
	u = strings.Replace(u, "ws://", "http://", 1)
	return u
}

func isNotFound(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not found") || strings.Contains(s, "no_such_room")
}
