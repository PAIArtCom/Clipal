/**
 * Clipal Landing Page — js/main.js
 * Theme: Kinetic Structuralism & Quantum Control Plane
 * Modules: 3D Tilt · 18-CLI Matrix Filter · Takeover Inspector ·
 *          Web UI Tour · Copy Command · Real-time Telemetry Stream
 */

'use strict';

document.addEventListener('DOMContentLoaded', () => {
  const isZh = document.documentElement.lang.startsWith('zh');

  /* ============================================================
     1. 3D PERSPECTIVE KINETIC TILT (HERO STAGE)
     ============================================================ */
  const heroViewport = document.querySelector('.hero-console-viewport');
  const heroStage = document.querySelector('.hero-3d-stage');

  if (heroViewport && heroStage) {
    let ticking = false;

    heroViewport.addEventListener('mousemove', (e) => {
      if (!ticking) {
        requestAnimationFrame(() => {
          const rect = heroViewport.getBoundingClientRect();
          const x = e.clientX - rect.left;
          const y = e.clientY - rect.top;
          const centerX = rect.width / 2;
          const centerY = rect.height / 2;

          const rotateX = ((y - centerY) / centerY) * -12 + 6;
          const rotateY = ((x - centerX) / centerX) * 12 - 6;

          heroStage.style.transform = `rotateX(${rotateX.toFixed(2)}deg) rotateY(${rotateY.toFixed(2)}deg) translateZ(10px)`;
          ticking = false;
        });
        ticking = true;
      }
    });

    heroViewport.addEventListener('mouseleave', () => {
      heroStage.style.transform = `rotateX(8deg) rotateY(-8deg) translateZ(0)`;
    });
  }

  /* ============================================================
     2. COPY TO CLIPBOARD WITH KINETIC FEEDBACK
     ============================================================ */
  const copyButtons = document.querySelectorAll('[data-copy]');
  copyButtons.forEach((btn) => {
    btn.addEventListener('click', async () => {
      const textToCopy = btn.getAttribute('data-copy');
      if (!textToCopy) return;

      try {
        await navigator.clipboard.writeText(textToCopy);
        const originalContent = btn.innerHTML;
        const successMsg = isZh ? '✓ 已复制到剪贴板' : '✓ Copied to clipboard';

        btn.innerHTML = `<span class="text-signal-green" style="font-weight:600">${successMsg}</span>`;
        btn.style.borderColor = 'var(--signal-green)';

        setTimeout(() => {
          btn.innerHTML = originalContent;
          btn.style.borderColor = '';
        }, 2200);
      } catch (err) {
        console.error('Clipboard copy failed:', err);
      }
    });
  });

  /* ============================================================
     3. 18-CLI ORBITAL MATRIX FILTER & INSPECTOR MODAL
     ============================================================ */
  const matrixFilterBtns = document.querySelectorAll('.matrix-tab-btn');
  const cliCards = document.querySelectorAll('.cli-card');
  const modalOverlay = document.getElementById('takeover-modal');
  const modalTitle = document.getElementById('modal-cli-name');
  const modalTarget = document.getElementById('modal-cli-target');
  const modalDesc = document.getElementById('modal-cli-desc');
  const modalDiff = document.getElementById('modal-cli-diff');
  const modalClose = document.getElementById('modal-close');

  // Filter tabs
  matrixFilterBtns.forEach((btn) => {
    btn.addEventListener('click', () => {
      matrixFilterBtns.forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');

      const filter = btn.getAttribute('data-filter');
      cliCards.forEach((card) => {
        const category = card.getAttribute('data-category');
        if (filter === 'all' || category === filter) {
          card.style.display = 'flex';
        } else {
          card.style.display = 'none';
        }
      });
    });
  });

  // Modal open on card click
  cliCards.forEach((card) => {
    card.addEventListener('click', () => {
      if (!modalOverlay) return;
      const name = card.getAttribute('data-name');
      const target = card.getAttribute('data-target');
      const desc = card.getAttribute('data-details');
      const diffCode = card.getAttribute('data-diff');

      if (modalTitle) modalTitle.textContent = name;
      if (modalTarget) modalTarget.textContent = target;
      if (modalDesc) modalDesc.textContent = desc;
      if (modalDiff) {
        modalDiff.innerHTML = diffCode.split('\n').map((line, idx) => {
          const num = String(idx + 1).padStart(2, '0');
          const isAdd = line.trim().startsWith('+');
          const safeLine = line.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
          return `<div class="diff-line ${isAdd ? 'diff-add' : 'diff-keep'}"><span class="diff-num">${num}</span><span>${safeLine}</span></div>`;
        }).join('');
      }

      modalOverlay.classList.add('active');
    });
  });

  if (modalClose && modalOverlay) {
    modalClose.addEventListener('click', () => modalOverlay.classList.remove('active'));
    modalOverlay.addEventListener('click', (e) => {
      if (e.target === modalOverlay) modalOverlay.classList.remove('active');
    });
  }

  /* ============================================================
     4. SPATIAL WEB UI TOUR (TAB SWITCHER)
     ============================================================ */
  const tourBtns = document.querySelectorAll('.tour-tab-btn');
  const tourImgs = document.querySelectorAll('.tour-img');
  const tourTitle = document.getElementById('tour-title');
  const tourDesc = document.getElementById('tour-desc');
  const tourBadge = document.getElementById('tour-badge');

  const tourData = {
    providers: {
      titleZh: '服务商与密钥拓扑（Providers Topology）',
      titleEn: 'Providers Topology & Multi-Key Pool',
      descZh: '按客户端智能分组管理所有 API 提供商。自由调整 Auto 自动容灾模式或 Manual 强制置顶模式；单 Provider 轻松录入多行 Key，直观查看当前运行态：使用中、待命、冷却中（精确倒计时）、不可用及恢复探测状态。',
      descEn: 'Intelligently group and manage upstream providers by client. Seamlessly switch between Auto failover and Manual pinned modes, input multi-line keys per provider, and observe real-time health: active, standby, cooldown timer, and recovery probes.',
      badgeZh: '实时热重载 · 多 Key 轮询池',
      badgeEn: 'Hot Reload · Multi-Key Pool',
    },
    takeover: {
      titleZh: '18 家 CLI 一键接管舱（CLI Takeover Bay）',
      titleEn: '18-CLI Takeover Bay & Diff Inspector',
      descZh: '可视化管理所有受支持的 CLI 助手。Apply 前直观预览“当前文件”与“写入后文件”的逐行 Diff；发生环境冲突或自定义目录遮蔽时给出主动告警；随时一键完整 Rollback，恢复初态。',
      descEn: 'Visually manage all 18 supported CLI clients. Preview line-by-line Diff before applying; detect environment variable overrides or directory shadowing; rollback to the original byte-exact baseline with one click at any time.',
      badgeZh: '无损 AST 注入 · 秒级原子回滚',
      badgeEn: 'Lossless AST Merge · Atomic Rollback',
    },
    settings: {
      titleZh: '全局网络与安全防护（Network & Global Settings）',
      titleEn: 'Global Network & DoH Security',
      descZh: '配置本地与局域网访问边界。精细控制 HTTP/HTTPS/SOCKS5 代理链与 Cloudflare / Google DoH 安全解析回退；配置断路器阈值、桌面通知、超时机制及日志保留周期。',
      descEn: 'Configure network boundaries and remote access. Fine-tune SOCKS5/HTTP proxy inheritance, Cloudflare and Google DoH fallback, circuit breaker thresholds, desktop alerts, and audit log retentions.',
      badgeZh: 'DoH 加密解析 · 代理链路继承',
      badgeEn: 'Encrypted DoH · SOCKS5 / HTTP Proxy Chain',
    },
    status: {
      titleZh: '实时用量与请求遥测（Real-Time Telemetry）',
      titleEn: 'Real-Time Telemetry & Cost Engine',
      descZh: '毫秒级观察每一次 LLM 交互链路。完整记录客户端来源、上游目标、端到端响应耗时、状态码、Token 吞吐量与微美元费用归因，彻底告别黑盒盲猜。',
      descEn: 'Sub-millisecond observability for every LLM interaction. Real-time logging of client origins, upstream targets, end-to-end latency, status codes, token throughput, and exact micro-USD cost attribution.',
      badgeZh: '微美元精准归因 · 零开销遥测',
      badgeEn: 'Micro-USD Pricing · Zero Overhead',
    },
  };

  tourBtns.forEach((btn) => {
    btn.addEventListener('click', () => {
      tourBtns.forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');

      const targetKey = btn.getAttribute('data-tour');
      tourImgs.forEach((img) => {
        if (img.getAttribute('data-tour-img') === targetKey) {
          img.classList.add('active');
        } else {
          img.classList.remove('active');
        }
      });

      const data = tourData[targetKey];
      if (data) {
        if (tourTitle) tourTitle.textContent = isZh ? data.titleZh : data.titleEn;
        if (tourDesc) tourDesc.textContent = isZh ? data.descZh : data.descEn;
        if (tourBadge) tourBadge.textContent = isZh ? data.badgeZh : data.badgeEn;
      }
    });
  });

  /* ============================================================
     5. INSTALLATION PROTOCOL TABS
     ============================================================ */
  const installTabs = document.querySelectorAll('.install-tab-btn');
  const installPanels = document.querySelectorAll('.install-panel');

  installTabs.forEach((tab) => {
    tab.addEventListener('click', () => {
      installTabs.forEach((t) => t.classList.remove('active'));
      tab.classList.add('active');

      const targetId = tab.getAttribute('data-install-tab');
      installPanels.forEach((panel) => {
        if (panel.id === `install-panel-${targetId}`) {
          panel.classList.add('active');
        } else {
          panel.classList.remove('active');
        }
      });
    });
  });

  /* ============================================================
     6. REAL-TIME TELEMETRY CONSOLE STREAM TICKER
     ============================================================ */
  const consoleBody = document.getElementById('hero-console-stream');
  if (consoleBody) {
    const logs = [
      { text: '→ [claude-code]  POST /clipal  →  claude-3-7-sonnet  ✓  241ms', cls: 'success' },
      { text: '→ [opencode]     POST /clipal  →  gemini-2.5-pro       ✓  312ms', cls: 'route' },
      { text: '→ [codex]        POST /clipal  →  gpt-4.1 [429 LIMIT]  ✗  18ms', cls: 'warn' },
      { text: '                 FAILOVER PIVOT → Azure_Backup [KEY_2] ✓  389ms', cls: 'accent' },
      { text: '→ [aider]         POST /clipal  →  deepseek-chat        ✓  195ms', cls: 'route' },
      { text: '→ [goose]         POST /clipal  →  claude-3-7-sonnet    ✓  278ms', cls: 'success' },
    ];

    let logIdx = 0;
    setInterval(() => {
      const log = logs[logIdx % logs.length];
      logIdx++;

      const line = document.createElement('div');
      line.className = `terminal-line ${log.cls}`;
      line.textContent = log.text;

      consoleBody.appendChild(line);
      if (consoleBody.children.length > 7) {
        consoleBody.removeChild(consoleBody.children[0]);
      }
    }, 2800);
  }

  /* ============================================================
     7. LIVE PRICING CALCULATION TICKER (PILLAR 4)
     ============================================================ */
  const pricingVal = document.getElementById('pricing-ticker-val');
  if (pricingVal) {
    let baseMicros = 42189;
    setInterval(() => {
      baseMicros += Math.floor(Math.random() * 120) + 15;
      const dollars = (baseMicros / 1000000).toFixed(6);
      pricingVal.textContent = `$${dollars}`;
    }, 1800);
  }
});
