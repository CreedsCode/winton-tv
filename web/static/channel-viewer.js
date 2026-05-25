// Channel viewer: LiveKit room (subscribe video/audio) + chat via
// DataChannel. Anonymous viewers receive chat but token's
// CanPublishData=false stops them from sending. Server enforces.
//
// V1.8 additions:
//   - Viewer count in chat header (driven by Room participant events)
//   - Channel pill in chat (linkable /<slug> for senders who own a channel)
//   - Owner crown 👑 (for sender whose slug == this channel)
//   - Cam icon 📹 (for sender currently streaming elsewhere; polled API)

(function () {
  const TAG = '[viewer]';
  function log() {
    const args = Array.prototype.slice.call(arguments);
    args.unshift(TAG);
    console.log.apply(console, args);
  }

  function ready(fn) {
    if (document.readyState !== 'loading') fn();
    else document.addEventListener('DOMContentLoaded', fn);
  }

  ready(function () {
    const cfgEl = document.getElementById('lk-config');
    if (!cfgEl) { console.error(TAG, 'no #lk-config'); return; }

    let cfg;
    try { cfg = JSON.parse(cfgEl.textContent); }
    catch (e) { console.error(TAG, 'lk-config parse failed', e); return; }
    log('config', { url: cfg.url, room: cfg.room, canChat: cfg.canChat });

    if (!window.LivekitClient) {
      console.error(TAG, 'livekit-client SDK failed to load');
      return;
    }

    const videoEl     = document.getElementById('lk-video');
    const overlay     = document.getElementById('player-overlay');
    const unmuteBtn   = document.getElementById('unmute-hint');
    const liveBadge   = document.querySelector('.live-badge');
    const chatList    = document.getElementById('chat-messages');
    const chatForm    = document.getElementById('chat-form');
    const chatInput   = document.getElementById('chat-input');
    const viewerCount = document.getElementById('viewer-count');

    // Set of slugs currently live elsewhere (excluding current room).
    // Refreshed every 30s; chat lookups use this for cam icons.
    let liveSlugs = new Set();

    function refreshLiveSlugs() {
      if (!cfg.liveStreamsURL) return;
      fetch(cfg.liveStreamsURL, { headers: { 'Accept': 'application/json' } })
        .then(function (r) { return r.json(); })
        .then(function (data) {
          if (data && Array.isArray(data.slugs)) {
            liveSlugs = new Set(data.slugs);
          }
        })
        .catch(function (e) { log('liveStreams refresh failed', e); });
    }
    refreshLiveSlugs();
    setInterval(refreshLiveSlugs, 30000);

    function setBadgeLive(isLive) {
      if (!liveBadge) return;
      liveBadge.classList.toggle('is-live', isLive);
      liveBadge.classList.toggle('is-offline', !isLive);
      liveBadge.innerHTML = isLive
        ? '<span class="live-dot"></span>LIVE'
        : 'OFFLINE';
    }

    function showOffline() {
      overlay.classList.remove('is-connecting');
      overlay.classList.add('is-offline');
      overlay.querySelector('.overlay-content').innerHTML =
        '<div class="overlay-label">Offline</div>' +
        '<div class="overlay-sub">No one’s streaming right now.</div>';
      overlay.hidden = false;
      overlay.style.display = '';
    }

    function showConnecting() {
      overlay.classList.remove('is-offline');
      overlay.classList.add('is-connecting');
      overlay.querySelector('.overlay-content').innerHTML =
        '<div class="spinner"></div><div class="overlay-label">Connecting…</div>';
      overlay.hidden = false;
      overlay.style.display = '';
    }

    function hideOverlay() {
      log('hide overlay');
      overlay.hidden = true;
      overlay.style.display = 'none';
      overlay.classList.remove('is-connecting', 'is-offline');
    }

    // Belt-and-suspenders: also hide overlay once the <video> actually
    // starts rendering frames (covers SDK-timing edge cases where
    // TrackSubscribed fires before the overlay element is settled).
    videoEl.addEventListener('playing', function () {
      log('video.playing event — hiding overlay');
      hideOverlay();
    });

    // Manual escape hatch — click overlay to dismiss if it gets stuck.
    overlay.addEventListener('click', function () {
      log('overlay clicked — manual dismiss');
      hideOverlay();
    });
    overlay.style.cursor = 'pointer';

    function isPublisher(p) {
      return p && p.identity === cfg.room;
    }

    // ─────────────── Viewer count ───────────────
    function updateViewerCount() {
      if (!viewerCount) return;
      let n = 0;
      room.remoteParticipants.forEach(function (p) { if (!isPublisher(p)) n++; });
      // include ourselves
      n += 1;
      viewerCount.textContent = String(n);
    }

    // ─────────────── Chat helpers ───────────────
    // V1.8.1: chat is now DB-backed via /api/chat. History loads on
    // page join, new messages stream in via SSE. Removed the LiveKit
    // DataChannel chat path entirely.

    function escapeHTML(s) {
      return String(s).replace(/[&<>"']/g, function (c) {
        return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c];
      });
    }

    function colorFor(s) {
      let h = 0;
      for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
      return 'hsl(' + h + ', 65%, 72%)';
    }

    function initialOf(name) {
      const t = (name || '?').trim();
      return t.length ? t[0].toUpperCase() : '?';
    }

    // Track rendered message IDs to dedupe (history + SSE can overlap
    // if SSE catches a message that just landed in history).
    const renderedIds = new Set();

    // renderChat takes a normalised message:
    //   { id, name, avatarUrl, slug, isOwner, text, identity }
    function renderChat(m) {
      if (m.id != null && renderedIds.has(m.id)) return;
      if (m.id != null) renderedIds.add(m.id);

      const identity = m.identity || ('u-' + (m.slug || m.name || 'x'));
      const color = colorFor(identity);
      const isLiveElsewhere = m.slug && m.slug !== cfg.room && liveSlugs.has(m.slug);

      const row = document.createElement('div');
      row.className = 'chat-msg' + (m.isOwner ? ' is-owner' : '');

      const avatar = document.createElement('div');
      avatar.className = 'chat-avatar';
      if (m.avatarUrl) {
        const img = document.createElement('img');
        img.src = m.avatarUrl;
        img.alt = '';
        avatar.appendChild(img);
      } else {
        avatar.textContent = initialOf(m.name);
        avatar.style.background = 'hsl(' + colorFor(m.name).match(/\d+/)[0] + ', 30%, 22%)';
        avatar.style.color = color;
      }

      const body = document.createElement('div');
      body.className = 'chat-body';

      const header = document.createElement('div');
      header.className = 'chat-line';

      if (m.isOwner) {
        const crown = document.createElement('span');
        crown.className = 'chat-crown';
        crown.title = 'Channel owner';
        crown.textContent = '👑';
        header.appendChild(crown);
      }

      const nameEl = document.createElement('span');
      nameEl.className = 'chat-name';
      nameEl.style.color = color;
      nameEl.textContent = m.name;
      header.appendChild(nameEl);

      if (m.slug) {
        const pill = document.createElement('a');
        pill.className = 'chat-pill';
        pill.href = '/u/' + m.slug;
        pill.textContent = '/' + m.slug;
        if (isLiveElsewhere) {
          pill.classList.add('is-live');
          const cam = document.createElement('span');
          cam.className = 'chat-cam';
          cam.title = 'Live now';
          cam.textContent = '📹';
          pill.appendChild(cam);
        }
        header.appendChild(pill);
      }

      body.appendChild(header);

      const textEl = document.createElement('div');
      textEl.className = 'chat-text';
      textEl.innerHTML = escapeHTML(m.text);
      body.appendChild(textEl);

      row.appendChild(avatar);
      row.appendChild(body);

      const nearBottom =
        chatList.scrollHeight - chatList.scrollTop - chatList.clientHeight < 80;
      chatList.appendChild(row);
      if (nearBottom) chatList.scrollTop = chatList.scrollHeight;
    }

    function renderFromAPI(srv) {
      renderChat({
        id: srv.id,
        name: srv.sender_name,
        avatarUrl: srv.sender_avatar_url,
        slug: srv.sender_slug,
        isOwner: srv.is_owner,
        text: srv.text,
        identity: 'u-' + srv.sender_user_id,
      });
    }

    function renderSystem(text) {
      const row = document.createElement('div');
      row.className = 'chat-system';
      row.textContent = text;
      chatList.appendChild(row);
      chatList.scrollTop = chatList.scrollHeight;
    }

    // Load last 50 messages on join.
    fetch('/api/chat/' + cfg.room + '/history?limit=50', { headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.json(); })
      .then(function (data) {
        (data.messages || []).forEach(renderFromAPI);
      })
      .catch(function (e) { log('chat history failed', e); });

    // Live updates via Server-Sent Events. EventSource auto-reconnects
    // on transient network blips.
    let chatSSE = null;
    function startChatStream() {
      try {
        chatSSE = new EventSource('/api/chat/' + cfg.room + '/stream');
        chatSSE.addEventListener('chat', function (ev) {
          try {
            const msg = JSON.parse(ev.data);
            renderFromAPI(msg);
          } catch (e) { log('chat SSE parse', e); }
        });
        chatSSE.onerror = function (ev) {
          log('chat SSE error (will retry)', ev);
        };
      } catch (e) {
        log('chat SSE failed to start', e);
      }
    }
    startChatStream();

    // ─────────────── Room ───────────────
    const room = new LivekitClient.Room({
      adaptiveStream: true,
      dynacast: true,
    });

    room.on(LivekitClient.RoomEvent.Connected, function () {
      log('room connected');
      updateViewerCount();
    });

    room.on(LivekitClient.RoomEvent.Disconnected, function (reason) {
      log('room disconnected', reason);
      showOffline();
      setBadgeLive(false);
    });

    room.on(LivekitClient.RoomEvent.ParticipantConnected, function (p) {
      log('participant connected', p.identity);
      if (isPublisher(p)) showConnecting();
      updateViewerCount();
    });

    room.on(LivekitClient.RoomEvent.ParticipantDisconnected, function (p) {
      log('participant disconnected', p.identity);
      if (isPublisher(p)) {
        showOffline();
        setBadgeLive(false);
      }
      updateViewerCount();
    });

    room.on(LivekitClient.RoomEvent.TrackSubscribed, function (track, pub, p) {
      log('track subscribed', { from: p.identity, kind: track.kind });
      if (track.kind === LivekitClient.Track.Kind.Video) {
        track.attach(videoEl);
        hideOverlay();
        setBadgeLive(true);
        if (videoEl.muted) unmuteBtn.hidden = false;
        videoEl.play().catch(function () {});
      } else if (track.kind === LivekitClient.Track.Kind.Audio) {
        track.attach(videoEl);
      }
    });

    room.on(LivekitClient.RoomEvent.TrackUnsubscribed, function (track) {
      track.detach().forEach(function (el) { el.remove(); });
    });

    // V1.8.1: DataChannel chat removed. Chat now goes through
    // /api/chat/{slug} (POST send) and SSE /api/chat/{slug}/stream
    // (receive). See the chat helpers section above.

    unmuteBtn.addEventListener('click', function () {
      videoEl.muted = false;
      videoEl.play().catch(function () {});
      unmuteBtn.hidden = true;
    });

    if (chatForm && cfg.canChat) {
      chatForm.addEventListener('submit', function (e) {
        e.preventDefault();
        const text = chatInput.value.trim();
        if (!text) return;
        // URL-encoded, not FormData. The server's ChatSend calls r.ParseForm()
        // before r.FormValue("text"), which for multipart/form-data bodies
        // (what FormData produces) initialises r.Form to a non-nil empty map
        // *without* reading the body — and FormValue then short-circuits the
        // ParseMultipartForm path. Net effect: server reads "" and 400s with
        // "empty message". Matching the rest of the codebase by sending
        // application/x-www-form-urlencoded sidesteps the trap.
        const body = new URLSearchParams();
        body.append('text', text);
        // Disable submit while in-flight (rapid duplicate clicks)
        const submitBtn = chatForm.querySelector('button[type="submit"]');
        if (submitBtn) submitBtn.disabled = true;
        chatInput.value = '';
        fetch('/api/chat/' + cfg.room, {
          method: 'POST',
          body: body,
          credentials: 'same-origin',
        }).then(function (r) {
          if (submitBtn) submitBtn.disabled = false;
          if (!r.ok) {
            chatInput.value = text; // restore on failure
            return r.text().then(function (t) { renderSystem('Send failed: ' + (t || r.status)); });
          }
          // Success: server fans out via SSE; renderedIds dedupe handles
          // showing it just once.
        }).catch(function (err) {
          if (submitBtn) submitBtn.disabled = false;
          chatInput.value = text;
          log('chat send err', err);
          renderSystem('Failed to send. Try again.');
        });
      });
    }

    log('connecting...');
    room.connect(cfg.url, cfg.token, { autoSubscribe: true })
      .then(function () {
        updateViewerCount();
        const pubInRoom = Array.from(room.remoteParticipants.values()).some(isPublisher);
        if (!pubInRoom) {
          showOffline();
          setBadgeLive(false);
        } else {
          showConnecting();
        }
      })
      .catch(function (err) {
        console.error(TAG, 'connect failed', err);
        showOffline();
        setBadgeLive(false);
      });
  });
})();
