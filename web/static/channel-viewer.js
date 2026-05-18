// Channel viewer: LiveKit room (subscribe video/audio) + chat via
// DataChannel. Anonymous viewers receive chat but token's
// CanPublishData=false stops them from sending. Server enforces.

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

    const videoEl   = document.getElementById('lk-video');
    const overlay   = document.getElementById('player-overlay');
    const unmuteBtn = document.getElementById('unmute-hint');
    const liveBadge = document.querySelector('.live-badge');
    const chatList  = document.getElementById('chat-messages');
    const chatForm  = document.getElementById('chat-form');
    const chatInput = document.getElementById('chat-input');

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
      overlay.hidden = true;
      overlay.style.display = 'none';
    }

    function isPublisher(p) {
      return p && p.identity === cfg.room;
    }

    // ─────────────── Chat ───────────────
    const encoder = new TextEncoder();
    const decoder = new TextDecoder();

    function escapeHTML(s) {
      return String(s).replace(/[&<>"']/g, function (c) {
        return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c];
      });
    }

    function colorFor(identity) {
      // deterministic color per identity (HSL hue from a simple hash)
      let h = 0;
      for (let i = 0; i < identity.length; i++) h = (h * 31 + identity.charCodeAt(i)) % 360;
      return 'hsl(' + h + ', 65%, 72%)';
    }

    function parseMeta(raw) {
      if (!raw) return {};
      try { return JSON.parse(raw); } catch (e) { return {}; }
    }

    function avatarInitial(name) {
      const t = (name || '?').trim();
      return t.length ? t[0].toUpperCase() : '?';
    }

    function renderChat(participant, text, opts) {
      opts = opts || {};
      const isSelf = !!opts.self;
      const name = (participant && participant.name) || (isSelf ? 'You' : 'Guest');
      const meta = parseMeta(participant && participant.metadata);
      const color = colorFor((participant && participant.identity) || 'self');

      const row = document.createElement('div');
      row.className = 'chat-msg';

      const avatar = document.createElement('div');
      avatar.className = 'chat-avatar';
      if (meta.avatar_url) {
        const img = document.createElement('img');
        img.src = meta.avatar_url;
        img.alt = '';
        avatar.appendChild(img);
      } else {
        avatar.textContent = avatarInitial(name);
        avatar.style.background = 'hsl(' + colorFor(name).match(/\d+/)[0] + ', 30%, 22%)';
        avatar.style.color = color;
      }

      const body = document.createElement('div');
      body.className = 'chat-body';
      body.innerHTML =
        '<span class="chat-name" style="color:' + color + '">' + escapeHTML(name) + '</span>' +
        '<span class="chat-text">' + escapeHTML(text) + '</span>';

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
    });

    room.on(LivekitClient.RoomEvent.Disconnected, function (reason) {
      log('room disconnected', reason);
      showOffline();
      setBadgeLive(false);
    });

    room.on(LivekitClient.RoomEvent.ParticipantConnected, function (p) {
      log('participant connected', p.identity);
      if (isPublisher(p)) showConnecting();
    });

    room.on(LivekitClient.RoomEvent.ParticipantDisconnected, function (p) {
      log('participant disconnected', p.identity);
      if (isPublisher(p)) {
        showOffline();
        setBadgeLive(false);
      }
    });

    room.on(LivekitClient.RoomEvent.TrackSubscribed, function (track, pub, p) {
      log('track subscribed', {
        from: p.identity,
        kind: track.kind,
        source: pub.source,
        mimeType: pub.mimeType,
        muted: track.isMuted,
      });
      if (track.kind === LivekitClient.Track.Kind.Video) {
        track.attach(videoEl);
        hideOverlay();
        setBadgeLive(true);
        if (videoEl.muted) unmuteBtn.hidden = false;

        // Inspect video element state right after attach
        log('video attached', {
          srcObjectSet: !!videoEl.srcObject,
          videoWidth: videoEl.videoWidth,
          videoHeight: videoEl.videoHeight,
          readyState: videoEl.readyState,  // 0=nothing, 4=enough data
          paused: videoEl.paused,
          muted: videoEl.muted,
        });

        // Force playback in case autoplay was suppressed.
        videoEl.play().then(function () {
          log('video.play() resolved');
        }).catch(function (e) {
          log('video.play() rejected', e.name, e.message);
        });

        // Re-inspect a moment later — frames should be arriving.
        setTimeout(function () {
          log('video state @ +2s', {
            videoWidth: videoEl.videoWidth,
            videoHeight: videoEl.videoHeight,
            readyState: videoEl.readyState,
            currentTime: videoEl.currentTime,
            paused: videoEl.paused,
            networkState: videoEl.networkState,
          });
        }, 2000);
      } else if (track.kind === LivekitClient.Track.Kind.Audio) {
        track.attach(videoEl);
      }
    });

    // Surface native video element issues
    ['playing', 'waiting', 'stalled', 'suspend', 'error', 'loadedmetadata', 'canplay'].forEach(function (ev) {
      videoEl.addEventListener(ev, function () {
        log('video.' + ev, {
          videoWidth: videoEl.videoWidth,
          videoHeight: videoEl.videoHeight,
          readyState: videoEl.readyState,
          err: videoEl.error && {
            code: videoEl.error.code,
            message: videoEl.error.message,
          },
        });
      });
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

    // Send chat — only wired when canChat=true (server enforces too).
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
