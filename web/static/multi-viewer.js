// Multi-view: one LiveKit Room per cell. Cells are pre-rendered with
// slug + token data attrs by the server. JS walks them on load and
// attaches a video track when each room's publisher subscribes.
//
// Audio handling: every cell starts MUTED. Audio tracks are NOT
// attached automatically — they're held in `attachedAudioTrack` and
// only routed to a dedicated hidden <audio> sink when the user clicks
// the cell's mute toggle. This makes muting reliable across browsers
// (using <video muted> alone isn't enough — LiveKit's track.attach
// can route audio through a path that ignores it).

(function () {
  function ready(fn) {
    if (document.readyState !== 'loading') fn();
    else document.addEventListener('DOMContentLoaded', fn);
  }

  ready(function () {
    const cfgEl = document.getElementById('lk-config');
    if (!cfgEl) return;
    const cfg = JSON.parse(cfgEl.textContent);
    if (!window.LivekitClient) {
      console.error('livekit-client SDK failed to load');
      return;
    }

    document.querySelectorAll('.multi-cell').forEach(function (cell) {
      const slug   = cell.dataset.slug;
      const token  = cell.dataset.token;
      const video  = cell.querySelector('.multi-video');
      const muteBt = cell.querySelector('.multi-mute');

      // Dedicated audio sink, hidden. Audio tracks attach here, NOT to
      // <video>, so toggling .muted actually silences playback.
      const audio = document.createElement('audio');
      audio.setAttribute('playsinline', '');
      audio.muted = true;
      audio.style.display = 'none';
      cell.appendChild(audio);

      // Defence in depth — keep the <video> element itself muted too.
      video.muted = true;

      let muted = true;
      let attachedAudioTrack = null;

      function applyMuteUI() {
        muteBt.textContent = muted ? '🔇' : '🔊';
        muteBt.classList.toggle('is-active', !muted);
        muteBt.title = muted ? 'Unmute' : 'Mute';
      }
      applyMuteUI();

      function isPublisher(p) { return p && p.identity === slug; }

      const room = new LivekitClient.Room({
        adaptiveStream: true,
        dynacast: true,
      });

      room.on(LivekitClient.RoomEvent.TrackSubscribed, function (track, pub, p) {
        if (!isPublisher(p)) return;
        if (track.kind === LivekitClient.Track.Kind.Video) {
          track.attach(video);
          video.muted = true; // re-assert; track.attach may reset
          cell.classList.add('is-live');
        } else if (track.kind === LivekitClient.Track.Kind.Audio) {
          attachedAudioTrack = track;
          if (!muted) {
            // User already unmuted before tracks arrived → attach now.
            track.attach(audio);
            audio.muted = false;
            audio.play().catch(function () {});
          }
        }
      });

      room.on(LivekitClient.RoomEvent.TrackUnsubscribed, function (track) {
        track.detach().forEach(function (el) { el.remove(); });
        if (track === attachedAudioTrack) attachedAudioTrack = null;
      });

      room.on(LivekitClient.RoomEvent.ParticipantDisconnected, function (p) {
        if (isPublisher(p)) cell.classList.remove('is-live');
      });

      muteBt.addEventListener('click', function (e) {
        e.preventDefault();
        muted = !muted;
        applyMuteUI();
        if (muted) {
          audio.pause();
          audio.muted = true;
          if (attachedAudioTrack) attachedAudioTrack.detach(audio);
        } else {
          if (attachedAudioTrack) attachedAudioTrack.attach(audio);
          audio.muted = false;
          audio.play().catch(function () {});
        }
      });

      room.connect(cfg.url, token, { autoSubscribe: true })
        .catch(function (err) {
          console.error('multi: connect failed for', slug, err);
          cell.classList.add('is-error');
        });
    });
  });
})();
