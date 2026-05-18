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
    const encoder = new TextEncoder();
    const decoder = new TextDecoder();

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

    function parseMeta(raw) {
      if (!raw) return {};
      try { return JSON.parse(raw); } catch (e) { return {}; }
    }

    function initialOf(name) {
      const t = (name || '?').trim();
      return t.length ? t[0].toUpperCase() : '?';
    }

    function renderChat(participant, text, opts) {
      opts = opts || {};
      const isSelf = !!opts.self;
      const name = (participant && participant.name) || (isSelf ? 'You' : 'Guest');
      const meta = parseMeta(participant && participant.metadata);
      const ident = (participant && participant.identity) || 'self';
      const color = colorFor(ident);
      const slug = meta.slug || '';
      const isOwner = !!meta.is_owner;
      const isLiveElsewhere = slug && slug !== cfg.room && liveSlugs.has(slug);

      const row = document.createElement('div');
      row.className = 'chat-msg' + (isOwner ? ' is-owner' : '');

      // Avatar
      const avatar = document.createElement('div');
      avatar.className = 'chat-avatar';
      if (meta.avatar_url) {
        const img = document.createElement('img');
        img.src = meta.avatar_url;
        img.alt = '';
        avatar.appendChild(img);
      } else {
        avatar.textContent = initialOf(name);
        avatar.style.background = 'hsl(' + colorFor(name).match(/\d+/)[0] + ', 30%, 22%)';
        avatar.style.color = color;
      }

      // Body
      const body = document.createElement('div');
      body.className = 'chat-body';

      // Header row: crown, name, slug pill, cam icon
      const header = document.createElement('div');
      header.className = 'chat-line';

      if (isOwner) {
        const crown = document.createElement('span');
        crown.className = 'chat-crown';
        crown.title = 'Channel owner';
        crown.textContent = '👑';
        header.appendChild(crown);
      }

      const nameEl = document.createElement('span');
      nameEl.className = 'chat-name';
      nameEl.style.color = color;
      nameEl.textContent = name;
      header.appendChild(nameEl);

      if (slug) {
        const pill = document.createElement('a');
        pill.className = 'chat-pill';
        // V1.8: link to profile, not channel. Channel is "watch", profile
        // is "who". Profile works whether or not they're live.
        pill.href = '/u/' + slug;
        pill.textContent = '/' + slug;
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
      textEl.innerHTML = escapeHTML(text);
      body.appendChild(textEl);

      row.appendChild(avatar);
      row.appendChild(body);

      const nearBottom =
        chatList.scrollHeight - chatList.scrollTop - chatList.clientHeight < 80;
      chatList.appendChild(row);
      if (nearBottom) chatList.scrollTop = chatList.scrollHeight;
    }

    function renderSystem(text) {
      const row = document.createElement('div');
      row.className = 'chat-system';
      row.textContent = text;
      chatList.appendChild(row);
      chatList.scrollTop = chatList.scrollHeight;
    }

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

    room.on(LivekitClient.RoomEvent.DataReceived, function (payload, participant) {
      let msg;
      try { msg = JSON.parse(decoder.decode(payload)); }
      catch (e) { log('bad chat payload', e); return; }
      if (msg && msg.type === 'chat' && typeof msg.text === 'string') {
        renderChat(participant, msg.text);
      }
    });

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
        const payload = encoder.encode(JSON.stringify({ type: 'chat', text: text }));
        room.localParticipant.publishData(payload, { reliable: true }).then(function () {
          renderChat(room.localParticipant, text, { self: true });
          chatInput.value = '';
        }).catch(function (err) {
          log('publishData failed', err);
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
