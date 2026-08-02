import './style.css';
import appIcon from './assets/images/lulu-icon.png';
import { gsap } from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';
import {
  Activity,
  Check,
  CircleAlert,
  CircleCheck,
  createIcons,
  Minus,
  ShieldCheck,
  Square,
  Trash2,
  X,
} from 'lucide';
import { WindowMinimise, WindowToggleMaximise } from '../wailsjs/runtime/runtime';
import { ClearHistory, GetStatus, HideToTray, SetGuardianEnabled } from '../wailsjs/go/main/App';

gsap.registerPlugin(ScrollTrigger);

const icons = { Activity, Check, CircleAlert, CircleCheck, Minus, ShieldCheck, Square, Trash2, X };

const app = document.querySelector('#app');

app.innerHTML = `
  <main class="shell">
    <div id="titlebar" class="titlebar">
      <div class="window-brand">
        <img src="${appIcon}" alt="">
        <span>露露时光服残留专杀</span>
      </div>
      <div class="window-controls">
        <button id="window-minimise" type="button" title="最小化" aria-label="最小化">
          <i data-lucide="minus" aria-hidden="true"></i>
        </button>
        <button id="window-maximise" type="button" title="最大化" aria-label="最大化">
          <i data-lucide="square" aria-hidden="true"></i>
        </button>
        <button id="window-close" class="close" type="button" title="隐藏到托盘" aria-label="隐藏到托盘">
          <i data-lucide="x" aria-hidden="true"></i>
        </button>
      </div>
    </div>
    <header class="topbar">
      <div class="brand">
        <div class="brand-copy">
          <strong>进程守护</strong>
          <span>WOWCLASSIC.EXE</span>
        </div>
      </div>
      <div class="guard-control">
        <div class="control-copy">
          <strong>自动清理</strong>
          <span id="toggle-label">已关闭</span>
        </div>
        <label class="switch" aria-label="开启时光服残留守护">
          <input id="guardian-toggle" type="checkbox">
          <span class="switch-track"><span class="switch-thumb"></span></span>
        </label>
      </div>
    </header>

    <section class="guard-console">
      <div class="guard-summary">
        <span id="status-dot" class="status-beacon"></span>
        <div>
          <p id="state-kicker">守护已关闭</p>
          <h1 id="guard-headline">等待开启守护</h1>
          <div class="scan-line">
            <span id="scan-time">尚未扫描</span>
            <span class="separator"></span>
            <span id="scan-interval">每 15 秒扫描一次</span>
          </div>
        </div>
      </div>

      <div class="metrics" aria-label="守护统计">
        <div class="metric-item healthy">
          <span>正常实例</span>
          <strong id="running-count">0</strong>
        </div>
        <div class="metric-item warning">
          <span>待清残留</span>
          <strong id="residual-count">0</strong>
        </div>
        <div class="metric-item cleaned">
          <span>累计清理</span>
          <strong id="killed-count">0</strong>
        </div>
      </div>
    </section>

    <section class="workspace">
      <div class="instances-pane">
        <div class="section-heading">
          <div>
            <h2>当前实例</h2>
            <span id="process-count">0 个进程</span>
          </div>
          <i data-lucide="activity" aria-hidden="true"></i>
        </div>
        <div id="process-list" class="process-list"></div>
      </div>

      <div class="history-pane">
        <div class="section-heading">
          <div>
            <h2>清理历史</h2>
            <span id="history-count">尚无记录</span>
          </div>
          <button id="clear-history" class="icon-button" type="button" title="清空清理历史" aria-label="清空清理历史">
            <i data-lucide="trash-2" aria-hidden="true"></i>
          </button>
        </div>
        <div id="history-list" class="history-list"></div>
      </div>
    </section>

    <footer class="activity-bar">
      <span id="activity-indicator"></span>
      <p id="activity">等待首次扫描</p>
    </footer>
  </main>
`;

const elements = {
  toggle: document.querySelector('#guardian-toggle'),
  toggleLabel: document.querySelector('#toggle-label'),
  stateKicker: document.querySelector('#state-kicker'),
  guardHeadline: document.querySelector('#guard-headline'),
  statusDot: document.querySelector('#status-dot'),
  scanTime: document.querySelector('#scan-time'),
  scanInterval: document.querySelector('#scan-interval'),
  runningCount: document.querySelector('#running-count'),
  residualCount: document.querySelector('#residual-count'),
  killedCount: document.querySelector('#killed-count'),
  processCount: document.querySelector('#process-count'),
  processList: document.querySelector('#process-list'),
  historyCount: document.querySelector('#history-count'),
  historyList: document.querySelector('#history-list'),
  clearHistory: document.querySelector('#clear-history'),
  activity: document.querySelector('#activity'),
  activityIndicator: document.querySelector('#activity-indicator'),
};

document.querySelector('#window-minimise').addEventListener('click', () => WindowMinimise());
document.querySelector('#window-maximise').addEventListener('click', () => WindowToggleMaximise());
document.querySelector('#window-close').addEventListener('click', () => HideToTray());
document.querySelector('#titlebar').addEventListener('dblclick', (event) => {
  if (!event.target.closest('.window-controls')) WindowToggleMaximise();
});

let updating = false;
let processSignature = '';
let historySignature = '';

function escapeHtml(value) {
  return String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}

function memoryLabel(memoryMB) {
  if (!memoryMB) return '内存读取中';
  if (memoryMB >= 1024) return `${(memoryMB / 1024).toFixed(1)} GB`;
  return `${memoryMB} MB`;
}

function renderProcesses(processes) {
  const signature = JSON.stringify(processes);
  if (signature === processSignature) return;
  processSignature = signature;

  if (processes.length === 0) {
    elements.processList.innerHTML = `
      <div class="empty-state compact">
        <i data-lucide="circle-check" aria-hidden="true"></i>
        <p>没有检测到游戏实例</p>
      </div>`;
    createIcons({ icons });
    return;
  }

  elements.processList.innerHTML = processes.map((process) => `
    <article class="instance-row ${process.status}">
      <span class="instance-state"></span>
      <div class="instance-copy">
        <div class="instance-title">
          <strong>PID ${process.pid}</strong>
          <span>${escapeHtml(process.statusLabel)}</span>
        </div>
        <p>${process.threads} 个线程 · ${memoryLabel(process.memoryMB)}</p>
        <small class="instance-path">${escapeHtml(process.path)}</small>
      </div>
      <i data-lucide="${process.status === 'running' ? 'shield-check' : 'circle-alert'}" aria-hidden="true"></i>
    </article>`).join('');

  createIcons({ icons });
  gsap.from(elements.processList.querySelectorAll('.instance-row'), {
    opacity: 0,
    x: -12,
    duration: 0.35,
    stagger: 0.05,
    ease: 'power2.out',
  });
}

function renderHistory(history) {
  const signature = history.map((record) => record.id).join('|');
  if (signature === historySignature) return;
  historySignature = signature;

  ScrollTrigger.getAll()
    .filter((trigger) => String(trigger.vars.id || '').startsWith('history-'))
    .forEach((trigger) => trigger.kill());

  if (history.length === 0) {
    elements.historyList.innerHTML = `
      <div class="empty-state">
        <span class="history-empty-mark"></span>
        <p>清理记录会出现在这里</p>
      </div>`;
    elements.clearHistory.disabled = true;
    return;
  }

  elements.clearHistory.disabled = false;
  elements.historyList.innerHTML = history.map((record) => {
    const [date, time] = record.cleanedAt.split(' ');
    return `
      <article class="history-row" data-id="${escapeHtml(record.id)}">
        <time><strong>${escapeHtml(time || record.cleanedAt)}</strong><span>${escapeHtml(date || '')}</span></time>
        <span class="timeline-node"><i data-lucide="check" aria-hidden="true"></i></span>
        <div class="history-copy">
          <strong>已清理退出残留</strong>
          <p>PID ${record.pid} · ${record.threads} 个线程</p>
        </div>
        <span class="memory-release">${memoryLabel(record.memoryMB)}</span>
      </article>`;
  }).join('');

  createIcons({ icons });
  requestAnimationFrame(() => {
    elements.historyList.querySelectorAll('.history-row').forEach((row, index) => {
      gsap.fromTo(row,
        { opacity: 0.18, scale: 0.975 },
        {
          opacity: 1,
          scale: 1,
          ease: 'none',
          scrollTrigger: {
            id: `history-${index}`,
            trigger: row,
            scroller: elements.historyList,
            start: 'top 96%',
            end: 'top 72%',
            scrub: 0.35,
          },
        });
    });
    ScrollTrigger.refresh();
  });
}

function render(status) {
  const running = status.processes.filter((process) => process.status === 'running').length;
  const residual = status.processes.filter((process) => process.status === 'residual').length;

  elements.toggle.checked = status.enabled;
  elements.toggleLabel.textContent = status.enabled ? '已开启' : '已关闭';
  elements.stateKicker.textContent = status.stateLabel;
  elements.guardHeadline.textContent = status.enabled ? '残留进程自动清理中' : '等待开启守护';
  elements.statusDot.className = `status-beacon ${status.enabled ? 'active' : ''}`;
  elements.scanTime.textContent = `上次扫描 ${status.lastScan}`;
  elements.scanInterval.textContent = `每 ${status.scanInterval} 秒扫描一次`;
  elements.runningCount.textContent = running;
  elements.residualCount.textContent = residual;
  elements.killedCount.textContent = status.totalKilled;
  elements.processCount.textContent = `${status.processes.length} 个进程`;
  elements.historyCount.textContent = status.history.length ? `${status.history.length} 条记录` : '尚无记录';

  renderProcesses(status.processes);
  renderHistory(status.history);

  if (status.lastError) {
    elements.activity.textContent = status.lastError;
    elements.activity.className = 'error';
    elements.activityIndicator.className = 'error';
  } else {
    elements.activity.textContent = status.lastAction || (status.enabled ? '守护进程工作正常' : '守护进程已暂停');
    elements.activity.className = '';
    elements.activityIndicator.className = status.enabled ? 'active' : '';
  }
}

async function refresh() {
  if (updating) return;
  updating = true;
  try {
    render(await GetStatus());
  } catch (error) {
    elements.activity.textContent = String(error);
    elements.activity.className = 'error';
  } finally {
    updating = false;
  }
}

elements.toggle.addEventListener('change', async () => {
  elements.toggle.disabled = true;
  try {
    render(await SetGuardianEnabled(elements.toggle.checked));
  } catch (error) {
    elements.toggle.checked = !elements.toggle.checked;
    elements.activity.textContent = String(error);
    elements.activity.className = 'error';
  } finally {
    elements.toggle.disabled = false;
  }
});

elements.clearHistory.addEventListener('click', async () => {
  if (updating || elements.clearHistory.disabled) return;
  updating = true;
  try {
    render(await ClearHistory());
  } finally {
    updating = false;
  }
});

createIcons({ icons });
gsap.from('.window-brand img', { opacity: 0, scale: 0.88, duration: 0.45, ease: 'power3.out' });
gsap.from('.guard-console', { opacity: 0, y: 8, duration: 0.5, ease: 'power3.out' });
refresh();
setInterval(refresh, 1500);
