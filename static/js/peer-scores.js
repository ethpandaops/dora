(function () {
  'use strict';

  var refreshTimer = null;
  var pollIntervalSeconds = 12;
  var selectedReporter = '';
  var clientTypeLabels = {
    1: 'lighthouse',
    2: 'lodestar',
    3: 'nimbus',
    4: 'prysm',
    5: 'teku',
    6: 'grandine',
    7: 'caplin'
  };

  document.addEventListener('DOMContentLoaded', function () {
    var dataEl = document.getElementById('peer-scores-config');
    if (dataEl) {
      var raw = parseInt(dataEl.getAttribute('data-poll-interval'), 10);
      if (!isNaN(raw) && raw > 0) {
        pollIntervalSeconds = raw;
      }
    }

    bindMatrixCells();
    bindReporterRows();
    bindGlobalReasonRefresh();
    refreshGlobalHistogram();

    scheduleAutoRefresh();
  });

  function bindMatrixCells() {
    var cells = document.querySelectorAll('.ps-cell[data-target]');
    for (var i = 0; i < cells.length; i++) {
      cells[i].addEventListener('click', onCellClick);
    }
  }

  function bindReporterRows() {
    var labels = document.querySelectorAll('.ps-reporter-label[data-reporter]');
    for (var i = 0; i < labels.length; i++) {
      labels[i].addEventListener('click', onReporterClick);
    }
  }

  function bindGlobalReasonRefresh() {
    var btn = document.getElementById('peer-scores-reasons-all');
    if (btn) {
      btn.addEventListener('click', function (ev) {
        ev.preventDefault();
        selectedReporter = '';
        var labels = document.querySelectorAll('.ps-reporter-label.active');
        for (var i = 0; i < labels.length; i++) {
          labels[i].classList.remove('active');
        }
        refreshGlobalHistogram();
      });
    }
  }

  function scheduleAutoRefresh() {
    if (refreshTimer) {
      window.clearInterval(refreshTimer);
    }
    var ms = pollIntervalSeconds * 1000;
    if (ms < 5000) {
      ms = 5000;
    }
    refreshTimer = window.setInterval(function () {
      refreshGlobalHistogram();
    }, ms);
  }

  function onReporterClick(ev) {
    var el = ev.currentTarget;
    var reporter = el.getAttribute('data-reporter') || '';
    selectedReporter = reporter;
    var labels = document.querySelectorAll('.ps-reporter-label.active');
    for (var i = 0; i < labels.length; i++) {
      labels[i].classList.remove('active');
    }
    el.classList.add('active');
    refreshGlobalHistogram();
  }

  function onCellClick(ev) {
    var el = ev.currentTarget;
    var reporter = el.getAttribute('data-reporter');
    var target = el.getAttribute('data-target');
    if (!reporter || !target) {
      return;
    }
    openDetailModal(reporter, target, el);
  }

  function openDetailModal(reporter, target, cellEl) {
    var modalEl = document.getElementById('peer-scores-detail');
    if (!modalEl) {
      return;
    }

    var title = document.getElementById('peer-scores-detail-title');
    if (title) {
      var reporterName = cellEl.getAttribute('data-reporter-name') || shortenPeer(reporter);
      var targetName = cellEl.getAttribute('data-target-name') || shortenPeer(target);
      title.textContent = reporterName + ' → ' + targetName;
    }

    var metaEl = document.getElementById('peer-scores-detail-meta');
    if (metaEl) {
      metaEl.innerHTML = buildMetaRow(cellEl);
    }

    var componentsEl = document.getElementById('peer-scores-detail-components');
    if (componentsEl) {
      componentsEl.innerHTML = buildComponentBars(cellEl);
    }

    var eventsEl = document.getElementById('peer-scores-detail-events');
    if (eventsEl) {
      eventsEl.innerHTML = '<div class="text-muted small">Loading events…</div>';
    }

    var modal = bootstrap.Modal.getOrCreateInstance(modalEl);
    modal.show();

    var since = Date.now() - 24 * 60 * 60 * 1000;
    var url = '/api/v1/clients/peer_scores/events?reporter=' + encodeURIComponent(reporter) +
      '&target=' + encodeURIComponent(target) +
      '&since=' + since +
      '&limit=50';
    fetch(url, { credentials: 'same-origin' })
      .then(function (resp) { return resp.json(); })
      .then(function (payload) {
        renderEventsTable(eventsEl, payload && payload.data ? payload.data : []);
      })
      .catch(function (err) {
        if (eventsEl) {
          eventsEl.innerHTML = '<div class="text-danger small">Failed to load events: ' +
            escapeHTML(String(err)) + '</div>';
        }
      });
  }

  function buildMetaRow(cellEl) {
    var score = cellEl.getAttribute('data-score') || '0';
    var normalized = cellEl.getAttribute('data-score-normalized') || '0';
    var state = cellEl.getAttribute('data-score-state') || 'unknown';
    var lastReason = cellEl.getAttribute('data-last-reason') || '';
    var lastNative = cellEl.getAttribute('data-last-native') || '';
    var lastAgo = cellEl.getAttribute('data-last-seconds-ago') || '';

    var html = '<dl class="row mb-0 small">';
    html += '<dt class="col-sm-3">Score</dt><dd class="col-sm-9">' +
      escapeHTML(score) + ' (normalized ' + escapeHTML(normalized) + ')</dd>';
    html += '<dt class="col-sm-3">State</dt><dd class="col-sm-9">' +
      escapeHTML(state) + '</dd>';
    if (lastReason) {
      html += '<dt class="col-sm-3">Last event</dt><dd class="col-sm-9"><code>' +
        escapeHTML(lastReason) + '</code>';
      if (lastNative && lastNative !== lastReason) {
        html += ' <span class="text-muted">(' + escapeHTML(lastNative) + ')</span>';
      }
      if (lastAgo) {
        html += ' <span class="text-muted">' + escapeHTML(lastAgo) + 's ago</span>';
      }
      html += '</dd>';
    }
    html += '</dl>';
    return html;
  }

  function buildComponentBars(cellEl) {
    var raw = cellEl.getAttribute('data-components') || '';
    if (!raw) {
      return '<div class="text-muted small">No component breakdown reported.</div>';
    }
    var entries = [];
    try {
      var parsed = JSON.parse(raw);
      for (var key in parsed) {
        if (Object.prototype.hasOwnProperty.call(parsed, key)) {
          entries.push({ name: key, value: parsed[key] });
        }
      }
    } catch (e) {
      return '<div class="text-muted small">Component data unavailable.</div>';
    }
    if (entries.length === 0) {
      return '<div class="text-muted small">No component breakdown reported.</div>';
    }
    entries.sort(function (a, b) { return a.name.localeCompare(b.name); });

    var html = '<div class="ps-components small">';
    for (var i = 0; i < entries.length; i++) {
      var ent = entries[i];
      var pct = Math.max(2, Math.min(100, Math.abs(ent.value)));
      var pos = ent.value >= 0;
      var color = pos ? 'bg-success' : 'bg-danger';
      html += '<div class="d-flex align-items-center mb-1">';
      html += '<div class="ps-component-name flex-shrink-0 me-2">' + escapeHTML(ent.name) + '</div>';
      html += '<div class="flex-grow-1 bg-light position-relative" style="height:14px;border-radius:3px;overflow:hidden;">';
      html += '<div class="' + color + '" style="width:' + pct + '%;height:100%;"></div>';
      html += '</div>';
      html += '<div class="ps-component-value text-end ms-2" style="min-width:5em;">' +
        escapeHTML(formatFloat(ent.value)) + '</div>';
      html += '</div>';
    }
    html += '</div>';
    return html;
  }

  function renderEventsTable(container, events) {
    if (!container) { return; }
    if (!events || events.length === 0) {
      container.innerHTML = '<div class="text-muted small">No events recorded in this window. ' +
        'Healthy peers on small devnets generally show no downscores.</div>';
      return;
    }
    var html = '<table class="table table-sm table-hover mb-0"><thead><tr>';
    html += '<th>When</th><th>Reason</th><th>Native</th><th class="text-end">Δ</th><th>Topic</th>';
    html += '</tr></thead><tbody>';
    for (var i = 0; i < events.length; i++) {
      var ev = events[i];
      var when = new Date(ev.observed_at).toLocaleTimeString();
      html += '<tr>';
      html += '<td class="text-nowrap">' + escapeHTML(when) + '</td>';
      html += '<td><code>' + escapeHTML(ev.reason_code) + '</code></td>';
      html += '<td class="text-muted">' + escapeHTML(ev.native_reason || '') + '</td>';
      var delta = ev.delta === null || ev.delta === undefined ? '' : formatFloat(ev.delta);
      html += '<td class="text-end">' + escapeHTML(delta) + '</td>';
      html += '<td>' + escapeHTML(ev.topic || '') + '</td>';
      html += '</tr>';
    }
    html += '</tbody></table>';
    container.innerHTML = html;
  }

  function refreshGlobalHistogram() {
    var container = document.getElementById('peer-scores-reason-histogram');
    if (!container) { return; }

    var since = Date.now() - 24 * 60 * 60 * 1000;
    var url = '/api/v1/clients/peer_scores/reasons?since=' + since;
    if (selectedReporter) {
      url += '&reporter=' + encodeURIComponent(selectedReporter);
    }

    fetch(url, { credentials: 'same-origin' })
      .then(function (resp) { return resp.json(); })
      .then(function (payload) {
        renderHistogram(container, payload && payload.data ? payload.data : {});
      })
      .catch(function () {
        container.innerHTML = '<div class="text-muted small">Failed to load reasons.</div>';
      });
  }

  function renderHistogram(container, counts) {
    var keys = Object.keys(counts);
    if (keys.length === 0) {
      container.innerHTML = '<div class="text-muted small">No reason events recorded yet. ' +
        'On a healthy small devnet this is expected.</div>';
      return;
    }
    keys.sort(function (a, b) { return counts[b] - counts[a]; });

    var max = counts[keys[0]];
    if (max < 1) { max = 1; }

    var html = '<div class="ps-histogram">';
    for (var i = 0; i < keys.length; i++) {
      var k = keys[i];
      var pct = Math.max(3, (counts[k] / max) * 100);
      var category = categoryFor(k);
      html += '<div class="d-flex align-items-center mb-1 small">';
      html += '<div class="ps-reason-name flex-shrink-0 me-2" title="' +
        escapeHTML(category) + '">' + escapeHTML(k) + '</div>';
      html += '<div class="flex-grow-1 bg-light position-relative" style="height:16px;border-radius:3px;overflow:hidden;">';
      html += '<div class="' + barClassForCategory(category) +
        '" style="width:' + pct + '%;height:100%;"></div>';
      html += '</div>';
      html += '<div class="ps-reason-count text-end ms-2" style="min-width:3em;">' +
        counts[k] + '</div>';
      html += '</div>';
    }
    html += '</div>';
    container.innerHTML = html;
  }

  function categoryFor(reason) {
    if (reason.indexOf('rpc_') === 0) { return 'rpc'; }
    if (reason.indexOf('gossip_') === 0) { return 'gossip'; }
    if (reason.indexOf('sync_') === 0) { return 'sync'; }
    if (reason.indexOf('status_') === 0) { return 'status'; }
    if (reason.indexOf('reward_') === 0) { return 'reward'; }
    if (reason === 'colocation' || reason === 'behaviour_penalty' || reason === 'das_bad_column_intersection') {
      return 'connection';
    }
    return 'other';
  }

  function barClassForCategory(category) {
    switch (category) {
      case 'rpc': return 'bg-warning';
      case 'gossip': return 'bg-danger';
      case 'sync': return 'bg-info';
      case 'status': return 'bg-primary';
      case 'reward': return 'bg-success';
      case 'connection': return 'bg-secondary';
      default: return 'bg-dark';
    }
  }

  function shortenPeer(peer) {
    if (!peer) { return '?'; }
    if (peer.length <= 12) { return peer; }
    return peer.substring(0, 6) + '…' + peer.substring(peer.length - 4);
  }

  function formatFloat(value) {
    if (typeof value !== 'number' || isNaN(value)) { return ''; }
    if (Math.abs(value) >= 1000) { return value.toFixed(2); }
    if (Math.abs(value) >= 1) { return value.toFixed(4); }
    return value.toFixed(6);
  }

  function escapeHTML(value) {
    if (value === null || value === undefined) { return ''; }
    return String(value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  window.peerScoresClientTypeLabel = function (id) {
    return clientTypeLabels[id] || 'unknown';
  };
})();
