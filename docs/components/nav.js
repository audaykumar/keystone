(function () {
  const el = document.getElementById('site-nav');
  if (!el) return;

  // Resolve the docs root from this script, independent of page depth.
  const root = new URL('../', document.currentScript.src);
  const indexUrl = new URL('index.html', root);
  const logoUrl = new URL('assets/keystone-logo.svg', root);

  if (!document.querySelector('link[rel="icon"]')) {
    const icon = document.createElement('link');
    icon.rel = 'icon';
    icon.type = 'image/svg+xml';
    icon.href = logoUrl;
    document.head.appendChild(icon);
  }

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

      .nav-logo {
        align-items: center;
        display: inline-flex;
        gap: 8px;
      }

      .nav-logo-mark {
        display: block;
        height: 28px;
        width: 28px;
      }

      .nav-logo-text {
        color: var(--text, #e6edf3);
        font-weight: 800;
        letter-spacing: 0;
      }
    `;
    document.head.appendChild(styles);
  }

  el.innerHTML = `
    <nav class="site-nav">
      <a class="nav-logo" href="${indexUrl}" aria-label="Keystone home">
        <img class="nav-logo-mark" src="${logoUrl}" alt="" />
        <span class="nav-logo-text">keystone</span>
      </a>
      <span class="nav-sep">|</span>
      <span class="nav-desc">Distributed systems &amp; fintech, built and broken.</span>
      <a class="nav-home sticky-home" href="${indexUrl}">← All topics</a>
    </nav>
  `;
})();
