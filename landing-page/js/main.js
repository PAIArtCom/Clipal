/* Progressive enhancement. All factual and translated content lives in HTML. */
(() => {
  'use strict';
  document.documentElement.classList.add('js');
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');

  const header = document.querySelector('.site-header');
  if (header) {
    const onScroll = () => header.classList.toggle('is-scrolled', window.scrollY > 8);
    window.addEventListener('scroll', onScroll, { passive: true });
    onScroll();
  }

  // The ticker loops by translating two identical lists; the clone stays hidden from assistive tech.
  document.querySelectorAll('[data-ticker-clone]').forEach(clone => {
    clone.innerHTML = clone.previousElementSibling.innerHTML;
  });

  const revealed = [...document.querySelectorAll('[data-reveal]')];
  if ('IntersectionObserver' in window && !reducedMotion.matches) {
    const observer = new IntersectionObserver(entries => {
      entries.forEach(entry => {
        if (!entry.isIntersecting) return;
        entry.target.classList.add('is-in');
        observer.unobserve(entry.target);
      });
    }, { rootMargin: '0px 0px -8% 0px' });
    revealed.forEach(element => observer.observe(element));
  } else revealed.forEach(element => element.classList.add('is-in'));

  // A local illustration only: no network requests or invented live telemetry.
  document.querySelectorAll('[data-hero-demo]').forEach(demo => {
    const clients = [...demo.querySelectorAll('[data-hero-client]')];
    const protocols = [...demo.querySelectorAll('.protocols > span')];
    const phases = [...demo.querySelectorAll('[data-hero-phase]')];
    const scenes = [...demo.querySelectorAll('[data-hero-scene]')];
    const markers = [...demo.querySelectorAll('.hero-phase-track i')];
    const keys = [...demo.querySelectorAll('[data-hero-key]')];
    const providers = [...demo.querySelectorAll('[data-hero-provider]')];
    const toggle = demo.querySelector('[data-hero-toggle]');
    const incoming = [
      'M65 0V26Q65 40 79 40H206Q220 40 220 54V82',
      'M220 0V82',
      'M375 0V26Q375 40 361 40H234Q220 40 220 54V82'
    ];
    const outgoing = [
      'M220 0V16Q220 30 206 30H79Q65 30 65 44V64',
      'M220 0V64',
      'M220 0V16Q220 30 234 30H361Q375 30 375 44V64'
    ];
    const durations = [1500, 1500, 1900, 1800, 2400];
    let phase = 0;
    let scene = 0;
    let client = 0;
    let playing = !reducedMotion.matches;
    let visible = false;
    let timer;
    const currentFrames = () => scenes.length
      ? [...scenes[scene].querySelectorAll('[data-hero-frame]')]
      : phases;
    const schedule = () => {
      clearTimeout(timer);
      const running = playing && visible && !document.hidden;
      demo.classList.toggle('is-playing', running);
      const motion = Number(currentFrames()[phase]?.dataset.motion ?? phase);
      if (running) timer = setTimeout(advance, durations[motion]);
    };
    const render = () => {
      const frames = currentFrames();
      const frame = frames[phase];
      demo.dataset.phase = frame?.dataset.motion ?? String(phase);
      if (scenes.length) {
        demo.dataset.scene = scenes[scene].dataset.heroScene;
        scenes.forEach((item, index) => { item.hidden = index !== scene; });
        demo.querySelector('[data-hero-scene-label]').textContent = scenes[scene].dataset.label;
      }
      clients.forEach((button, index) => button.setAttribute('aria-pressed', String(index === client)));
      protocols.forEach((item, index) => item.classList.toggle('is-current', index === Number(clients[client].dataset.protocol)));
      demo.querySelectorAll('.incoming-lines .active-line, .incoming-lines .route-packet').forEach(path => path.setAttribute('d', incoming[client]));
      demo.querySelectorAll('.outgoing-lines .active-line, .outgoing-lines .route-packet').forEach(path => path.setAttribute('d', outgoing[Number(clients[client].dataset.protocol)]));
      frames.forEach((caption, index) => { caption.hidden = index !== phase; });
      markers.forEach((marker, index) => {
        marker.hidden = index >= frames.length;
        marker.classList.toggle('is-done', index <= phase);
      });
      demo.querySelector('.hero-phase-index').textContent = `${String(phase + 1).padStart(2, '0')} / ${String(frames.length).padStart(2, '0')}`;
      if (providers.length === 2) {
        demo.querySelectorAll('[data-provider-channel]').forEach(label => {
          label.textContent = clients[client].dataset.channel;
        });
        demo.querySelector('[data-provider-account]').textContent = clients[client].dataset.oauthAccount;
        const labels = { ready: 'READY', active: 'ROUTING', limited: '429', cooldown: 'COOLDOWN', success: '200 OK', standby: 'STANDBY', sticky: 'STICKY' };
        Object.keys(labels).forEach(state => {
          labels[state] = demo.getAttribute(`data-state-${state}`) || labels[state];
        });
        providers.forEach((provider, index) => {
          const state = frame.dataset[index === 0 ? 'api' : 'oauth'];
          provider.dataset.state = state;
          provider.querySelector('b').textContent = labels[state];
        });
      } else if (keys.length === 2) {
        // Compatibility for older page markup that may still be cached.
        keys[0].dataset.state = phase >= 2 ? 'limited' : 'ready';
        keys[0].querySelector('b').textContent = phase >= 2 ? '429' : 'READY';
        keys[1].dataset.state = phase === 4 ? 'success' : phase === 3 ? 'active' : 'standby';
        keys[1].querySelector('b').textContent = phase === 4 ? '200 OK' : phase === 3 ? 'RETRY' : 'STANDBY';
      }
      toggle.textContent = playing ? toggle.dataset.pauseLabel : toggle.dataset.playLabel;
      schedule();
    };
    function advance() {
      phase = (phase + 1) % currentFrames().length;
      if (phase === 0) {
        scene = scenes.length ? (scene + 1) % scenes.length : 0;
        // Retain one channel through normal → fallback → sticky, then rotate tools.
        if (scene === 0) client = (client + 1) % clients.length;
      }
      render();
    }
    clients.forEach((button, index) => {
      button.disabled = false;
      button.addEventListener('click', () => { client = index; phase = 0; scene = 0; render(); });
    });
    toggle.addEventListener('click', () => { playing = !playing; render(); });
    demo.querySelector('[data-hero-next]').addEventListener('click', () => { playing = false; advance(); });
    document.addEventListener('visibilitychange', schedule);
    reducedMotion.addEventListener('change', () => { playing = !reducedMotion.matches; render(); });
    if ('IntersectionObserver' in window) {
      new IntersectionObserver(entries => {
        visible = entries[0].isIntersecting;
        schedule();
      }, { threshold: 0.15 }).observe(demo);
    } else visible = true;
    demo.querySelector('.hero-demo-controls').hidden = false;
    demo.classList.add('hero-demo-ready');
    render();
  });

  const menu = document.querySelector('.menu-toggle');
  const navigation = document.querySelector('#navigation');
  if (header && menu && navigation) {
    header.classList.add('nav-ready');
    const setMenu = (open, restoreFocus = false) => {
      menu.setAttribute('aria-expanded', String(open));
      menu.setAttribute('aria-label', open ? menu.dataset.closeLabel : menu.dataset.openLabel);
      navigation.classList.toggle('is-open', open);
      if (restoreFocus) menu.focus();
    };
    menu.addEventListener('click', () => setMenu(menu.getAttribute('aria-expanded') !== 'true'));
    navigation.addEventListener('click', event => {
      if (event.target.closest('a')) setMenu(false);
    });
    document.addEventListener('keydown', event => {
      if (event.key === 'Escape' && menu.getAttribute('aria-expanded') === 'true') setMenu(false, true);
    });
    document.addEventListener('click', event => {
      if (!header.contains(event.target)) setMenu(false);
    });
    window.matchMedia('(min-width: 861px)').addEventListener('change', () => setMenu(false));
  }

  document.querySelectorAll('[data-tabs]').forEach(group => {
    const tablist = group.querySelector('.tab-list');
    const tabs = [...tablist.querySelectorAll('[data-tab]')];
    const panels = [...group.querySelectorAll('[data-tab-panel]')];
    if (!tabs.length || tabs.length !== panels.length) return;
    tablist.setAttribute('role', 'tablist');
    const select = (index, focus = false) => {
      tabs.forEach((tab, i) => {
        tab.setAttribute('aria-selected', String(i === index));
        tab.tabIndex = i === index ? 0 : -1;
        panels[i].hidden = i !== index;
      });
      if (focus) tabs[index].focus();
    };
    tabs.forEach((tab, index) => {
      const panel = document.getElementById(tab.dataset.tab);
      tab.setAttribute('role', 'tab');
      tab.setAttribute('aria-controls', panel.id);
      panel.setAttribute('role', 'tabpanel');
      panel.setAttribute('aria-labelledby', tab.id);
      panel.tabIndex = 0;
      tab.addEventListener('click', () => select(index));
      tab.addEventListener('keydown', event => {
        const nextIndex = (index + 1) % tabs.length;
        const previousIndex = (index + tabs.length - 1) % tabs.length;
        const moves = { ArrowRight: nextIndex, ArrowDown: nextIndex, ArrowLeft: previousIndex, ArrowUp: previousIndex, Home: 0, End: tabs.length - 1 };
        if (Object.hasOwn(moves, event.key)) {
          event.preventDefault();
          select(moves[event.key], true);
        }
      });
    });
    select(0);
    group.classList.add('tabs-ready');
  });

  const toast = document.querySelector('.toast');
  let toastTimer;
  let copySequence = 0;
  const buttonTimers = new WeakMap();
  const announce = message => {
    clearTimeout(toastTimer);
    toast.textContent = message;
    toast.classList.add('is-visible');
    toastTimer = setTimeout(() => toast.classList.remove('is-visible'), 4500);
  };
  document.querySelectorAll('[data-copy-target]').forEach(button => {
    const initialContent = button.innerHTML;
    button.addEventListener('click', async () => {
      const sequence = ++copySequence;
      const source = document.getElementById(button.dataset.copyTarget);
      try {
        if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable');
        await navigator.clipboard.writeText(source.textContent);
        if (sequence !== copySequence) return;
        clearTimeout(buttonTimers.get(button));
        button.textContent = button.dataset.success;
        announce(document.body.dataset.copySuccess);
        buttonTimers.set(button, setTimeout(() => { button.innerHTML = initialContent; }, 2400));
      } catch {
        if (sequence !== copySequence) return;
        announce(document.body.dataset.copyFailure);
        const range = document.createRange();
        range.selectNodeContents(source);
        const selection = window.getSelection();
        selection.removeAllRanges();
        selection.addRange(range);
      }
    });
  });

  const dialog = document.querySelector('.screenshot-dialog');
  let zoomTrigger;
  if (dialog && typeof dialog.showModal === 'function') {
    const image = dialog.querySelector('img');
    const fullImageLink = dialog.querySelector('.full-image-link');
    document.querySelectorAll('[data-zoom]').forEach(button => {
      button.addEventListener('click', () => {
        const source = button.querySelector('img');
        image.src = source.currentSrc || source.src;
        image.alt = source.alt;
        fullImageLink.href = image.src;
        zoomTrigger = button;
        dialog.showModal();
        document.body.classList.add('dialog-open');
        dialog.querySelector('.dialog-close').focus();
      });
    });
    dialog.querySelector('.dialog-close').addEventListener('click', () => dialog.close());
    dialog.addEventListener('click', event => {
      if (event.target !== dialog) return;
      const bounds = dialog.getBoundingClientRect();
      if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) dialog.close();
    });
    dialog.addEventListener('close', () => {
      document.body.classList.remove('dialog-open');
      zoomTrigger?.focus({ preventScroll: true });
    });
  } else {
    document.querySelectorAll('[data-zoom]').forEach(button => {
      button.addEventListener('click', () => window.open(button.querySelector('img').src, '_blank', 'noopener'));
    });
  }

  document.querySelectorAll('[data-route-demo]').forEach(demo => {
    let step = 0;
    const keys = [...demo.querySelectorAll('[data-route-key]')];
    const captions = [...demo.querySelectorAll('[data-route-caption]')];
    const next = demo.querySelector('.route-next');
    demo.classList.add('route-demo-ready');
    next.addEventListener('click', () => {
      step = (step + 1) % keys.length;
      keys.forEach((key, index) => {
        key.classList.toggle('is-current', index === step);
        key.classList.toggle('is-tried', index < step);
        key.querySelector('.key-state').textContent = index === step ? demo.dataset.selected : index < step ? demo.dataset.tried : demo.dataset.standby;
        captions[index].hidden = index !== step;
      });
      next.firstChild.textContent = (step === keys.length - 1 ? demo.dataset.replay : demo.dataset.next) + ' ';
    });
  });
})();
