(function () {
  const el = document.getElementById('site-nav');
  if (!el) return;

  // Resolve the docs root from this script, independent of page depth.
  const root = new URL('../', document.currentScript.src);
  const indexUrl = new URL('index.html', root);

  if (!document.getElementById('sticky-nav-styles')) {
    const styles = document.createElement('style');
    styles.id = 'sticky-nav-styles';
    styles.textContent = `
      .sticky-home {
        position: fixed;
        top: 14px;
        right: 20px;
        z-index: 1000;
        padding: 7px 11px;
        border: 1px solid var(--border, #30363d);
        border-radius: 7px;
        background: var(--surface, #161b22);
        box-shadow: 0 4px 16px rgb(0 0 0 / 25%);
      }

      @media (max-width: 640px) {
        .site-nav {
          padding-right: 128px;
        }

        .sticky-home {
          top: 10px;
          right: 12px;
        }
      }
    `;
    document.head.appendChild(styles);
  }

  el.innerHTML = `
    <nav class="site-nav">
      <a class="nav-logo" href="${indexUrl}">⬡ keystone</a>
      <span class="nav-sep">—</span>
      <span class="nav-desc">Distributed systems &amp; fintech, built and broken.</span>
      <a class="nav-home sticky-home" href="${indexUrl}">← All topics</a>
    </nav>
  `;
})();
