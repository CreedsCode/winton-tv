// Multi-view: one LiveKit Room per cell. Cells are pre-rendered with
// slug + token data attrs by the server. JS walks them on load and
// attaches a video track when each room's publisher subscribes.

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

      const room = new LivekitClient.Room({
        adaptiveStream: true,
        dynacast: true,
      });

      function isPublisher(p) { return p && p.identity === slug; }

      room.on(LivekitClient.RoomEvent.TrackSubscribed, function (track, pub, p) {
        if (!isPublisher(p)) return;
        if (track.kind === LivekitClient.Track.Kind.Video) {
          track.attach(video);
          cell.classList.add('is-live');
        } else if (track.kind === LivekitClient.Track.Kind.Audio) {
          track.attach(video);
        }
      });

      room.on(LivekitClient.RoomEvent.ParticipantDisconnected, function (p) {
        if (isPublisher(p)) cell.classList.remove('is-live');
      });

      muteBt.addEventListener('click', function () {
        video.muted = !video.muted;
        muteBt.textContent = video.muted ? '🔇' : '🔊';
        if (!video.muted) video.play().catch(function () {});
      });

      room.connect(cfg.url, token, { autoSubscribe: true })
        .catch(function (err) {
          console.error('multi: connect failed for', slug, err);
          cell.classList.add('is-error');
        });
    });
  });
})();
