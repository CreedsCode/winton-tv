// Channel viewer: connect to LiveKit room, attach the first video track
// to the <video> element, surface a click-to-unmute hint (browsers block
// autoplay-with-sound). Read config from a <script id="lk-config">
// JSON blob written by the template so we don't have to escape into JS.

(function () {
  const TAG = '[viewer]';
  function log(/* ...args */) {
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
    log('config', { url: cfg.url, room: cfg.room, tokenPresent: !!cfg.token });

    if (!window.LivekitClient) {
      console.error(TAG, 'livekit-client SDK failed to load');
      return;
    }

    const videoEl   = document.getElementById('lk-video');
    const overlay   = document.getElementById('player-overlay');
    const unmuteBtn = document.getElementById('unmute-hint');
    const liveBadge = document.querySelector('.live-badge');

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
    }

    function showConnecting() {
      overlay.classList.remove('is-offline');
      overlay.classList.add('is-connecting');
      overlay.querySelector('.overlay-content').innerHTML =
        '<div class="spinner"></div><div class="overlay-label">Connecting…</div>';
      overlay.hidden = false;
    }

    function hideOverlay() {
      log('hide overlay');
      overlay.hidden = true;
      // Belt-and-suspenders against stale class state hiding the video.
      overlay.style.display = 'none';
    }

    const room = new LivekitClient.Room({
      adaptiveStream: true,
      dynacast: true,
    });

    // Wire EVERY interesting room event with logs so debugging from
    // DevTools is one-shot.
    room.on(LivekitClient.RoomEvent.Connected, function () {
      log('room connected', {
        publishers: Array.from(room.remoteParticipants.values()).map(function (p) {
          return { identity: p.identity, tracks: p.trackPublications.size };
        })
      });
    });

    room.on(LivekitClient.RoomEvent.Disconnected, function (reason) {
      log('room disconnected', reason);
      showOffline();
      setBadgeLive(false);
    });

    room.on(LivekitClient.RoomEvent.ParticipantConnected, function (p) {
      log('participant connected', p.identity);
      if (p.identity && !p.identity.startsWith('guest-')) {
        showConnecting();
      }
    });

    room.on(LivekitClient.RoomEvent.ParticipantDisconnected, function (p) {
      log('participant disconnected', p.identity);
      if (p.identity && !p.identity.startsWith('guest-')) {
        showOffline();
        setBadgeLive(false);
      }
    });

    room.on(LivekitClient.RoomEvent.TrackPublished, function (pub, p) {
      log('track published', { from: p.identity, kind: pub.kind, source: pub.source });
    });

    room.on(LivekitClient.RoomEvent.TrackSubscribed, function (track, pub, p) {
      log('track subscribed', { from: p.identity, kind: track.kind, source: pub.source });
      if (track.kind === LivekitClient.Track.Kind.Video) {
        track.attach(videoEl);
        hideOverlay();
        setBadgeLive(true);
        if (videoEl.muted) unmuteBtn.hidden = false;
      } else if (track.kind === LivekitClient.Track.Kind.Audio) {
        track.attach(videoEl);
      }
    });

    room.on(LivekitClient.RoomEvent.TrackUnsubscribed, function (track) {
      log('track unsubscribed', track.kind);
      track.detach().forEach(function (el) { el.remove(); });
    });

    room.on(LivekitClient.RoomEvent.TrackSubscriptionFailed, function (sid, p) {
      log('track subscription failed', { sid: sid, from: p && p.identity });
    });

    room.on(LivekitClient.RoomEvent.ConnectionQualityChanged, function (q, p) {
      log('quality', { from: p && p.identity, q: q });
    });

    unmuteBtn.addEventListener('click', function () {
      videoEl.muted = false;
      videoEl.play().catch(function (e) { log('unmute play err', e); });
      unmuteBtn.hidden = true;
    });

    log('connecting...');
    room.connect(cfg.url, cfg.token, { autoSubscribe: true })
      .then(function () {
        const publishers = Array.from(room.remoteParticipants.values())
          .filter(function (p) {
            return p.identity && !p.identity.startsWith('guest-');
          });
        log('connected, remote publishers:', publishers.length);
        if (publishers.length === 0) {
          showOffline();
          setBadgeLive(false);
        } else {
          // Publisher already in room — wait for TrackSubscribed (autoSubscribe
          // is on, tracks should subscribe within a tick).
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
