// Channel viewer: connect to LiveKit room, attach the first video track
// to the <video> element, surface a click-to-unmute hint (browsers block
// autoplay-with-sound). Read config from a <script id="lk-config">
// JSON blob written by the template so we don't have to escape into JS.

(function () {
  function ready(fn) {
    if (document.readyState !== 'loading') fn();
    else document.addEventListener('DOMContentLoaded', fn);
  }

  ready(function () {
    const cfgEl = document.getElementById('lk-config');
    if (!cfgEl) return;

    let cfg;
    try { cfg = JSON.parse(cfgEl.textContent); }
    catch (e) { console.error('lk-config parse failed', e); return; }

    if (!window.LivekitClient) {
      console.error('livekit-client SDK not loaded');
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

    function hideOverlay() { overlay.hidden = true; }

    const room = new LivekitClient.Room({
      adaptiveStream: true,
      dynacast: true,
    });

    room.on(LivekitClient.RoomEvent.TrackSubscribed, function (track) {
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
      track.detach().forEach(function (el) { el.remove(); });
    });

    room.on(LivekitClient.RoomEvent.ParticipantConnected, function (p) {
      // Publisher joined — likely about to send tracks. Stay in connecting
      // state until trackSubscribed fires.
      if (p.identity && !p.identity.startsWith('guest-')) {
        showConnecting();
      }
    });

    room.on(LivekitClient.RoomEvent.ParticipantDisconnected, function (p) {
      if (p.identity && !p.identity.startsWith('guest-')) {
        showOffline();
        setBadgeLive(false);
      }
    });

    room.on(LivekitClient.RoomEvent.Disconnected, function () {
      showOffline();
      setBadgeLive(false);
    });

    unmuteBtn.addEventListener('click', function () {
      videoEl.muted = false;
      videoEl.play().catch(function () {});
      unmuteBtn.hidden = true;
    });

    room.connect(cfg.url, cfg.token, { autoSubscribe: true })
      .then(function () {
        const publishers = Array.from(room.remoteParticipants.values())
          .filter(function (p) {
            return p.identity && !p.identity.startsWith('guest-');
          });
        if (publishers.length === 0) {
          showOffline();
          setBadgeLive(false);
        }
      })
      .catch(function (err) {
        console.error('LiveKit connect:', err);
        showOffline();
        setBadgeLive(false);
      });
  });
})();
