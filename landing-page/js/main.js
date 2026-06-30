/**
 * Clipal Landing Page — js/main.js
 * Modules: page-text · terminal · scroll-reveal · os-detect · platform-tabs · install-tabs · clipboard
 */
'use strict';

const PAGE_LANG = document.documentElement.lang.startsWith('zh') ? 'zh' : 'en';
const PAGE_TEXT = {
  en: {
    copied: 'Copied!',
    platform: {
      'mac-arm': 'Apple Silicon',
      'mac-intel': 'Intel Mac',
      'linux-x64': 'Linux x86_64',
      'linux-arm': 'Linux ARM64',
      windows: 'Windows',
    },
  },
  zh: {
    copied: '已复制！',
    platform: {
      'mac-arm': '苹果芯片',
      'mac-intel': 'Intel Mac',
      'linux-x64': 'Linux x86_64',
      'linux-arm': 'Linux ARM64',
      windows: 'Windows',
    },
  },
};

const TEXT = PAGE_TEXT[PAGE_LANG];

/* ============================================================
   1. TERMINAL ANIMATION
   ============================================================ */
const TERMINAL_SCRIPT = [
  { type: 'prompt', text: '$ clipal service start', delay: 400, speed: 42 },
  {
    type: 'ascii', text:
      `  ██████╗██╗     ██╗██████╗  █████╗ ██╗
 ██╔════╝██║     ██║██╔══██╗██╔══██╗██║
 ██║     ██║     ██║██████╔╝███████║██║
 ██║     ██║     ██║██╔═══╝ ██╔══██║██║
 ╚██████╗███████╗██║██║     ██║  ██║███████╗
  ╚═════╝╚══════╝╚═╝╚═╝     ╚═╝  ╚═╝╚══════╝`, delay: 200, instant: true
  },
  { type: 'blank', text: '', delay: 100 },
  { type: 'ok', text: '✓ Clipal running on http://127.0.0.1:3333', delay: 180, speed: 18 },
  { type: 'ok', text: '✓ Providers loaded: Anthropic, OpenAI (3), Gemini (2)', delay: 160, speed: 18 },
  { type: 'ok', text: '✓ Key pool: 5 keys active across 2 providers', delay: 500, speed: 18 },
  { type: 'blank', text: '', delay: 200 },
  { type: 'log', text: '→ [claude code]  POST /clipal  →  claude-4-6-sonnet  ✓  288ms', delay: 700, speed: 22 },
  { type: 'log', text: '→ [opencode]        POST /clipal  →  gemini-3.1-flash-lite       ✓  341ms', delay: 450, speed: 22 },
  { type: 'log-fail', text: '→ [codex]  POST /clipal  →  gpt-5.4 [QUOTA]   ✗', delay: 900, speed: 22 },
  { type: 'log-fo', text: '                           →  OpenAI  [FAILOVER]  ✓  519ms', delay: 280, speed: 22 },
];

function escHtml(value) {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

function buildLine(type, text) {
  const el = document.createElement('div');
  el.style.minHeight = '1.6em';

  switch (type) {
    case 'prompt':
      el.innerHTML = `<span class="t-prompt">${escHtml(text)}</span>`;
      break;
    case 'ascii':
      el.innerHTML = `<span class="t-ascii">${escHtml(text)}</span>`;
      break;
    case 'blank':
      el.innerHTML = ' ';
      break;
    case 'ok':
      el.innerHTML = `<span class="t-ok">${escHtml(text)}</span>`;
      break;
    case 'log': {
      const parts = text.split('✓');
      el.innerHTML = `<span class="t-arrow">→</span><span class="t-dim"> ${escHtml(parts[0].replace('→', '').trim())} </span>`
        + (parts[1] ? `<span class="t-ok">✓${escHtml(parts[1])}</span>` : '');
      break;
    }
    case 'log-fail':
      el.innerHTML = `<span class="t-arrow">→</span><span class="t-dim"> ${escHtml(text.replace('→', '').replace('✗', '').trim())} </span><span class="t-err">✗</span>`;
      break;
    case 'log-fo':
      el.innerHTML = '<span class="t-dim">                              </span><span class="t-arrow">→</span><span class="t-dim"> OpenAI </span><span class="t-tag">[FAILOVER]</span><span class="t-ok"> ✓  519ms</span>';
      break;
    default:
      el.textContent = text;
  }

  return el;
}

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function typewriteLine(el, outputEl, speed) {
  const template = document.createElement('div');
  template.innerHTML = el.innerHTML;
  el.innerHTML = '';
  outputEl.appendChild(el);

  for (const node of [...template.childNodes]) {
    if (node.nodeType === Node.TEXT_NODE) {
      const textNode = document.createTextNode('');
      el.appendChild(textNode);
      for (let i = 0; i < node.textContent.length; i += 1) {
        textNode.textContent = node.textContent.slice(0, i + 1);
        await sleep(speed + Math.random() * 6 - 3);
      }
      continue;
    }

    const span = node.cloneNode(false);
    el.appendChild(span);
    const fullText = node.textContent;
    for (let i = 0; i < fullText.length; i += 1) {
      span.textContent = fullText.slice(0, i + 1);
      await sleep(speed + Math.random() * 6 - 3);
    }
  }
}

async function runTerminal(outputEl) {
  for (const line of TERMINAL_SCRIPT) {
    await sleep(line.delay || 150);
    const el = buildLine(line.type, line.text);

    if (line.instant || line.type === 'blank' || line.type === 'ascii') {
      outputEl.appendChild(el);
    } else {
      await typewriteLine(el, outputEl, line.speed || 28);
    }

    outputEl.scrollTop = outputEl.scrollHeight;
  }

  const cursor = document.createElement('span');
  cursor.className = 't-cursor';
  cursor.setAttribute('aria-hidden', 'true');
  outputEl.appendChild(cursor);

  await sleep(5500);
  outputEl.innerHTML = '';
  runTerminal(outputEl);
}

function initTerminal() {
  const outputEl = document.getElementById('terminal-output');
  if (!outputEl) {
    return;
  }

  setTimeout(() => runTerminal(outputEl), 900);
}

/* ============================================================
   2. SCROLL REVEAL
   ============================================================ */
function initScrollReveal() {
  const observer = new IntersectionObserver(entries => {
    entries.forEach(entry => {
      if (!entry.isIntersecting) {
        return;
      }

      const siblings = [...entry.target.parentElement.querySelectorAll('.reveal')];
      const index = siblings.indexOf(entry.target);
      setTimeout(() => entry.target.classList.add('visible'), index * 80);
      observer.unobserve(entry.target);
    });
  }, { threshold: 0.08, rootMargin: '0px 0px -32px 0px' });

  document.querySelectorAll('.reveal').forEach(el => observer.observe(el));
}

/* ============================================================
   3. OS DETECTION & DOWNLOAD LINKS
   ============================================================ */
const RELEASE_BASE = 'https://github.com/PAIArtCom/Clipal/releases/latest/download';
const PLATFORM_CONFIG = {
  'mac-arm': { file: 'clipal-darwin-arm64', badge: 'Apple Silicon' },
  'mac-intel': { file: 'clipal-darwin-amd64', badge: 'Intel Mac' },
  'linux-x64': { file: 'clipal-linux-amd64', badge: 'Linux x86_64' },
  'linux-arm': { file: 'clipal-linux-arm64', badge: 'Linux ARM64' },
  windows: { file: 'clipal-windows-amd64.exe', badge: 'Windows' },
};

function detectPlatform() {
  const userAgent = (navigator.userAgent || '').toLowerCase();
  const platform = (navigator.userAgentData?.platform || navigator.platform || '').toLowerCase();

  if (userAgent.includes('win')) {
    return 'windows';
  }

  if (userAgent.includes('mac') || platform.includes('mac')) {
    const canvas = document.createElement('canvas');
    const gl = canvas.getContext('webgl');
    const debugInfo = gl && gl.getExtension('WEBGL_debug_renderer_info');
    const renderer = debugInfo ? gl.getParameter(debugInfo.UNMASKED_RENDERER_WEBGL) : '';

    return (renderer.toLowerCase().includes('apple') || navigator.maxTouchPoints > 0)
      ? 'mac-arm'
      : 'mac-intel';
  }

  if (userAgent.includes('linux')) {
    return (userAgent.includes('arm') || userAgent.includes('aarch64')) ? 'linux-arm' : 'linux-x64';
  }

  return null;
}

function applyPlatform(key) {
  const config = PLATFORM_CONFIG[key];
  if (!config) {
    return;
  }

  const url = `${RELEASE_BASE}/${config.file}`;

  const platformLabel = TEXT.platform[key] || config.badge;
  const binaryLabel = PAGE_LANG === 'zh'
    ? `${platformLabel} 二进制下载`
    : `Download ${platformLabel} binary`;

  const downloadButton = document.getElementById('download-direct-btn');
  const downloadText = document.getElementById('download-btn-text');
  if (downloadButton) {
    downloadButton.href = url;
  }
  if (downloadText) {
    downloadText.textContent = binaryLabel;
  }

  const manualButton = document.getElementById('manual-dl-btn');
  const manualText = document.getElementById('manual-dl-text');
  if (manualButton) {
    manualButton.href = url;
  }
  if (manualText) {
    manualText.textContent = binaryLabel;
  }

  document.querySelectorAll('.ptab').forEach(tab => {
    const active = tab.dataset.platform === key;
    tab.classList.toggle('active', active);
    tab.setAttribute('aria-selected', String(active));
  });
}

function initOSDetection() {
  const detectedPlatform = detectPlatform();
  if (detectedPlatform) {
    applyPlatform(detectedPlatform);
  }
}

/* ============================================================
   4. PLATFORM TABS
   ============================================================ */
function initPlatformTabs() {
  document.querySelectorAll('.ptab').forEach(tab => {
    tab.addEventListener('click', () => applyPlatform(tab.dataset.platform));
  });
}

/* ============================================================
   5. INSTALL TABS
   ============================================================ */
function initInstallTabs() {
  const tabs = document.querySelectorAll('.itab');
  const panels = document.querySelectorAll('.itab-panel');

  tabs.forEach(tab => {
    tab.addEventListener('click', () => {
      tabs.forEach(item => {
        item.classList.remove('active');
        item.setAttribute('aria-selected', 'false');
      });
      panels.forEach(panel => panel.classList.remove('active'));

      tab.classList.add('active');
      tab.setAttribute('aria-selected', 'true');

      const target = document.getElementById(`panel-${tab.dataset.tab}`);
      if (target) {
        target.classList.add('active');
      }
    });
  });
}

/* ============================================================
   6. CLIPBOARD COPY BUTTON
   ============================================================ */
async function writeClipboard(text) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const textArea = document.createElement('textarea');
  textArea.value = text;
  textArea.setAttribute('readonly', '');
  textArea.style.position = 'fixed';
  textArea.style.top = '-9999px';
  document.body.appendChild(textArea);
  textArea.select();
  document.execCommand('copy');
  textArea.remove();
}

function showCopiedState(button, label, restoreHtml = false) {
  const original = restoreHtml ? label.innerHTML : label.textContent;
  button.classList.add('copied');
  label.textContent = TEXT.copied;

  setTimeout(() => {
    button.classList.remove('copied');
    if (restoreHtml) {
      label.innerHTML = original;
    } else {
      label.textContent = original;
    }
  }, 2000);
}

function initClipboard() {
  const promptButton = document.getElementById('copy-ai-prompt');
  if (promptButton) {
    promptButton.addEventListener('click', async () => {
      const textEl = document.getElementById('ai-prompt-content');
      if (!textEl) {
        return;
      }

      try {
        await writeClipboard(textEl.textContent);
        const label = promptButton.querySelector('span');
        if (label) {
          showCopiedState(promptButton, label);
        }
      } catch (error) {
        console.warn('Clipboard API unavailable', error);
      }
    });
  }

  const commandButton = document.getElementById('cta-primary');
  if (commandButton) {
    commandButton.addEventListener('click', async () => {
      const command = commandButton.dataset.copyCommand;
      const label = document.getElementById('cta-primary-text');
      if (!command || !label) {
        return;
      }

      try {
        await writeClipboard(command);
        showCopiedState(commandButton, label, true);
      } catch (error) {
        console.warn('Clipboard API unavailable', error);
      }
    });
  }
}

/* ============================================================
   7. STICKY HEADER + NAV HIGHLIGHT
   ============================================================ */
function initHeaderScroll() {
  const header = document.getElementById('site-header');
  if (!header) {
    return;
  }

  window.addEventListener('scroll', () => {
    header.style.borderBottomColor = window.scrollY > 20
      ? 'rgba(212,175,55,0.14)'
      : 'rgba(255,255,255,0.08)';
  }, { passive: true });
}

function initNavHighlight() {
  const sections = document.querySelectorAll('section[id]');
  const navLinks = document.querySelectorAll('.nav-link[href^="#"]');
  const observer = new IntersectionObserver(entries => {
    entries.forEach(entry => {
      if (!entry.isIntersecting) {
        return;
      }

      navLinks.forEach(link => {
        link.style.color = link.getAttribute('href') === `#${entry.target.id}`
          ? 'var(--text-primary)'
          : '';
      });
    });
  }, { threshold: 0.35 });

  sections.forEach(section => observer.observe(section));
}

/* ============================================================
   8. INIT
   ============================================================ */
document.addEventListener('DOMContentLoaded', () => {
  document.body.classList.remove('is-loading');
  initTerminal();
  initScrollReveal();
  initOSDetection();
  initPlatformTabs();
  initInstallTabs();
  initClipboard();
  initHeaderScroll();
  initNavHighlight();
});
