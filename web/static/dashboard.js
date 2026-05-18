// Dashboard interactivity:
//   - copy-to-clipboard with visual confirmation
//   - show/hide for the secret token
//   - global toast on HTMX successful requests (e.g. settings saved)
// Vanilla JS, no deps beyond htmx (loaded by the template).

(function () {
  // ─────────────── copy + show/hide ───────────────
  function flash(btn, label, ms) {
    const original = btn.textContent;
    btn.textContent = label;
    btn.classList.add('btn-flash');
    setTimeout(function () {
      btn.textContent = original;
      btn.classList.remove('btn-flash');
    }, ms || 1500);
  }

  document.addEventListener('click', function (e) {
    const btn = e.target.closest('[data-copy-btn]');
    if (!btn) return;
    const input = btn.closest('.copy-row').querySelector('[data-copy]');
    if (!input || !input.value) {
      flash(btn, 'Nothing to copy', 1200);
      return;
    }
    const text = input.value;
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text)
        .then(function () { flash(btn, '✓ Copied'); })
        .catch(function () { fallbackCopy(input, btn); });
    } else {
      fallbackCopy(input, btn);
    }
  });

  function fallbackCopy(input, btn) {
    const wasType = input.type;
    input.type = 'text';
    input.select();
    try {
      document.execCommand('copy');
      flash(btn, '✓ Copied');
    } catch (err) {
      flash(btn, 'Copy failed', 1200);
    }
    input.type = wasType;
    window.getSelection().removeAllRanges();
  }

  document.addEventListener('click', function (e) {
    const btn = e.target.closest('[data-show-btn]');
    if (!btn) return;
    const input = btn.closest('.copy-row').querySelector('[data-secret]');
    if (!input) return;
    if (input.type === 'password') {
      input.type = 'text';
      btn.textContent = 'Hide';
    } else {
      input.type = 'password';
      btn.textContent = 'Show';
    }
  });

  // ─────────────── toast on HTMX success ───────────────
  let toastTimer = null;
  function showToast(text, kind) {
    const el = document.getElementById('toast');
    if (!el) return;
    el.textContent = text;
    el.className = 'toast is-' + (kind || 'ok');
    el.hidden = false;
    requestAnimationFrame(function () { el.classList.add('toast-in'); });
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () {
      el.classList.remove('toast-in');
      setTimeout(function () { el.hidden = true; }, 240);
    }, 1800);
  }

  // htmx fires events on document.body
  document.body.addEventListener('htmx:afterRequest', function (e) {
    const x = e.detail && e.detail.xhr;
    if (!x) return;
    if (x.status >= 200 && x.status < 300) {
      showToast('Saved', 'ok');
    } else {
      showToast('Save failed (' + x.status + ')', 'err');
    }
  });
})();
