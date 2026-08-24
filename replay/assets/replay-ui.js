// Replay control UI.
//
// This file is served by the dora-replay process and side-loaded into the explorer, so
// the explorer itself carries nothing but a script tag. Everything here talks to the
// replay's own control API on the origin this script came from.
(function () {
  "use strict";

  // the explorer hands us the control server's address; deriving it from this
  // script's own URL would depend on document.currentScript, which is not something
  // to rely on across browsers and script loading modes
  var base = (window.doraReplayApi || "").replace(/\/+$/, "");
  if (!base) {
    return;
  }

  var STORAGE_KEY = "dora-replay-open";

  // how often pages that poll for updates should refresh, relative to how fast the
  // replay is producing slots
  var REFRESH_MIN_MS = 1000;
  var REFRESH_MAX_MS = 15000;
  var REFRESH_STEPPING_MS = 2000;
  var SPEEDS = [
    { value: 0, label: "max" },
    { value: 0.5, label: "0.5x" },
    { value: 1, label: "1x" },
    { value: 2, label: "2x" },
    { value: 4, label: "4x" },
    { value: 8, label: "8x" },
    { value: 16, label: "16x" },
    { value: 32, label: "32x" },
  ];

  var CSS = [
    ".replay-callout{position:fixed;top:70px;right:16px;z-index:1010;max-width:calc(100vw - 32px);font-size:.8125rem}",
    ".replay-toggle{display:flex;align-items:center;gap:.5rem;margin-left:auto;padding:.25rem .75rem;border:1px solid var(--bs-border-color,#dee2e6);border-top:none;border-radius:0 0 .5rem .5rem;background:var(--bs-body-bg,#fff);color:var(--bs-body-color,#212529);box-shadow:0 .25rem .75rem rgba(0,0,0,.15);cursor:pointer;white-space:nowrap}",
    ".replay-toggle:hover{background:var(--bs-tertiary-bg,#f8f9fa)}",
    ".replay-tag{font-weight:600;letter-spacing:.04em;font-size:.6875rem;text-transform:uppercase;color:var(--bs-warning-text-emphasis,#997404)}",
    ".replay-dot{width:.5rem;height:.5rem;border-radius:50%;background:var(--bs-secondary,#6c757d);flex:none}",
    ".replay-dot.is-playing{background:var(--bs-success,#198754);animation:replay-pulse 1.6s ease-in-out infinite}",
    ".replay-dot.is-stepping{background:var(--bs-info,#0dcaf0)}",
    ".replay-dot.is-waiting{background:var(--bs-warning,#ffc107);animation:replay-pulse 1s ease-in-out infinite}",
    ".replay-dot.is-offline{background:var(--bs-danger,#dc3545)}",
    "@keyframes replay-pulse{0%,100%{opacity:1}50%{opacity:.35}}",
    ".replay-chevron{transition:transform .15s ease;font-size:.625rem}",
    ".replay-callout.is-open .replay-chevron{transform:rotate(180deg)}",
    ".replay-panel{display:none;width:23rem;max-width:calc(100vw - 32px);margin-top:.375rem;padding:.75rem;border:1px solid var(--bs-border-color,#dee2e6);border-radius:.5rem;background:var(--bs-body-bg,#fff);color:var(--bs-body-color,#212529);box-shadow:0 .5rem 1.5rem rgba(0,0,0,.2)}",
    ".replay-callout.is-open .replay-panel{display:block}",
    ".replay-grid{display:grid;grid-template-columns:auto 1fr;gap:.125rem .75rem;margin:0 0 .625rem}",
    ".replay-grid dt{color:var(--bs-secondary-color,#6c757d);font-weight:400}",
    ".replay-grid dd{margin:0;font-variant-numeric:tabular-nums;overflow:hidden;text-overflow:ellipsis}",
    ".replay-track{height:.375rem;border-radius:.25rem;background:var(--bs-tertiary-bg,#e9ecef);overflow:hidden;position:relative}",
    ".replay-track-fill{height:100%;background:var(--bs-primary,#0d6efd);width:0;transition:width .2s linear}",
    ".replay-track-target{position:absolute;top:0;bottom:0;width:2px;background:var(--bs-warning,#ffc107);display:none}",
    ".replay-range{display:flex;justify-content:space-between;color:var(--bs-secondary-color,#6c757d);font-size:.6875rem;margin:.25rem 0 .625rem}",
    ".replay-row{display:flex;flex-wrap:wrap;gap:.375rem;align-items:center;margin-bottom:.5rem}",
    ".replay-row .replay-btn,.replay-row select,.replay-row input{font-size:.8125rem;padding:.1875rem .5rem;border-radius:.25rem;border:1px solid var(--bs-border-color,#dee2e6);background:var(--bs-body-bg,#fff);color:var(--bs-body-color,#212529)}",
    ".replay-row .replay-btn{cursor:pointer}",
    ".replay-row .replay-btn:hover:not(:disabled){background:var(--bs-tertiary-bg,#f8f9fa)}",
    ".replay-row .replay-btn:disabled{opacity:.5;cursor:default}",
    ".replay-btn-main{background:var(--bs-primary,#0d6efd)!important;border-color:var(--bs-primary,#0d6efd)!important;color:#fff!important;min-width:4.5rem}",
    ".replay-seek-slot{flex:1;min-width:5rem;font-variant-numeric:tabular-nums}",
    ".replay-source{color:var(--bs-secondary-color,#6c757d);font-size:.6875rem;word-break:break-all}",
    ".replay-error{display:none;margin-top:.5rem;padding:.25rem .5rem;border-radius:.25rem;background:var(--bs-danger-bg-subtle,#f8d7da);color:var(--bs-danger-text-emphasis,#842029);font-size:.6875rem}",
    ".replay-error.is-shown{display:block}",
    "@media (max-width:575.98px){.replay-callout{right:8px;left:8px}.replay-panel{width:100%}}",
  ].join("");

  var state = null;
  var stateReadAt = 0;
  var connected = false;
  var el = {};

  function h(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function field(grid, label) {
    grid.appendChild(h("dt", null, label));
    var value = h("dd", null, "-");
    grid.appendChild(value);
    return value;
  }

  function build() {
    var style = h("style");
    style.textContent = CSS;
    document.head.appendChild(style);

    var root = h("div", "replay-callout");

    var toggle = h("button", "replay-toggle");
    toggle.type = "button";
    el.dot = h("span", "replay-dot");
    el.summary = h("span", "replay-summary", "connecting…");
    toggle.appendChild(el.dot);
    toggle.appendChild(h("span", "replay-tag", "replay"));
    toggle.appendChild(el.summary);
    toggle.appendChild(h("span", "replay-chevron", "▼"));
    toggle.addEventListener("click", function () {
      var open = !root.classList.contains("is-open");
      root.classList.toggle("is-open", open);
      try {
        window.localStorage.setItem(STORAGE_KEY, open ? "1" : "0");
      } catch (err) {
        /* private mode */
      }
    });
    root.appendChild(toggle);

    var panel = h("div", "replay-panel");

    var grid = h("dl", "replay-grid");
    el.slot = field(grid, "Slot");
    el.epoch = field(grid, "Epoch");
    el.time = field(grid, "Time");
    el.head = field(grid, "Head");
    el.checkpoints = field(grid, "Checkpoints");
    el.execution = field(grid, "EL block");
    panel.appendChild(grid);

    var track = h("div", "replay-track");
    el.fill = h("div", "replay-track-fill");
    el.marker = h("div", "replay-track-target");
    track.appendChild(el.fill);
    track.appendChild(el.marker);
    panel.appendChild(track);

    var range = h("div", "replay-range");
    el.rangeFrom = h("span", null, "");
    el.rangeTo = h("span", null, "");
    range.appendChild(el.rangeFrom);
    range.appendChild(el.rangeTo);
    panel.appendChild(range);

    var controls = h("div", "replay-row");
    el.toggleRun = button("Start", "replay-btn replay-btn-main", onToggleRun);
    el.step1 = button("+1", "replay-btn", function () {
      send({ action: "step", slots: 1 });
    });
    el.stepEpoch = button("+1 epoch", "replay-btn", function () {
      send({ action: "step", slots: (state && state.slots_per_epoch) || 32 });
    });
    el.speed = h("select");
    SPEEDS.forEach(function (speed) {
      var option = h("option", null, speed.label);
      option.value = String(speed.value);
      el.speed.appendChild(option);
    });
    el.speed.addEventListener("change", function () {
      send({ action: "speed", speed: parseFloat(el.speed.value) });
    });
    controls.appendChild(el.toggleRun);
    controls.appendChild(el.step1);
    controls.appendChild(el.stepEpoch);
    controls.appendChild(el.speed);
    panel.appendChild(controls);

    var seek = h("div", "replay-row");
    el.seekSlot = h("input", "replay-seek-slot");
    el.seekSlot.type = "number";
    el.seekSlot.placeholder = "slot";
    el.seekSlot.addEventListener("keydown", function (event) {
      if (event.key === "Enter") onSeek();
    });
    seek.appendChild(el.seekSlot);
    seek.appendChild(button("Forward to", "replay-btn", onSeek));
    panel.appendChild(seek);

    el.source = h("div", "replay-source", "");
    panel.appendChild(el.source);

    el.error = h("div", "replay-error");
    panel.appendChild(el.error);

    root.appendChild(panel);
    document.body.appendChild(root);
    el.root = root;

    try {
      if (window.localStorage.getItem(STORAGE_KEY) === "1") {
        root.classList.add("is-open");
      }
    } catch (err) {
      /* private mode */
    }
  }

  function button(label, className, handler) {
    var node = h("button", className, label);
    node.type = "button";
    node.addEventListener("click", handler);
    return node;
  }

  function onToggleRun() {
    if (!state) return;
    send(state.running ? { action: "stop" } : { action: "play", speed: parseFloat(el.speed.value) });
  }

  function onSeek() {
    var slot = parseInt(el.seekSlot.value, 10);
    if (!isFinite(slot)) {
      showError("enter a slot to run to");
      return;
    }
    send({ action: "forward", slot: slot, speed: parseFloat(el.speed.value) });
  }

  function send(command) {
    showError("");

    fetch(base + "/replay/command", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(command),
    })
      .then(function (response) {
        return response.json().then(function (body) {
          if (!response.ok) throw new Error(body.message || "command failed");
          return body;
        });
      })
      .then(apply)
      .catch(function (err) {
        showError(err.message || String(err));
      });
  }

  function showError(message) {
    el.error.textContent = message;
    el.error.classList.toggle("is-shown", !!message);
  }

  // tuneRefresh retunes the explorer's polling pages to the replay's pace. At 16x a
  // slot goes by in well under a second, so the stock 15s refresh would leave the
  // index page many epochs behind what the replay is actually showing.
  function tuneRefresh(status) {
    var interval = REFRESH_MAX_MS;

    if (status.running) {
      interval = status.rate > 0
        ? (status.slot_duration_ms || 12000) / status.rate
        : REFRESH_STEPPING_MS;
    }

    window.doraIndexRefreshInterval = Math.max(REFRESH_MIN_MS, Math.min(REFRESH_MAX_MS, interval));
  }

  function apply(status) {
    state = status;
    stateReadAt = Date.now();

    tuneRefresh(status);

    // only follow the server's speed when the user is not mid-selection
    if (document.activeElement !== el.speed) {
      var speed = String(status.speed);
      if (!SPEEDS.some(function (entry) { return String(entry.value) === speed; })) {
        var extra = h("option", null, speed + "x");
        extra.value = speed;
        el.speed.appendChild(extra);
      }
      el.speed.value = speed;
    }

    render();
  }

  // virtualNow interpolates the replay clock between status updates, so a playing
  // replay shows a moving clock rather than stepping once per event.
  function virtualNow() {
    if (!state) return null;
    var anchor = Date.parse(state.virtual_time);
    if (isNaN(anchor)) return null;
    if (!state.rate) return anchor;
    return anchor + (Date.now() - stateReadAt) * state.rate;
  }

  function slotAt(timeMs) {
    if (!state || !state.slot_duration_ms) return state ? state.virtual_slot : 0;
    var genesis = Date.parse(state.genesis_time);
    if (isNaN(genesis) || timeMs < genesis) return state.virtual_slot;
    return Math.floor((timeMs - genesis) / state.slot_duration_ms);
  }

  function mode() {
    if (!connected) return { label: "disconnected", dot: "is-offline" };
    if (!state) return { label: "connecting…", dot: "" };
    if (!state.running) return { label: "paused", dot: "" };
    if (state.holding) {
      return { label: "loading state" + (state.state_loads > 1 ? " (" + state.state_loads + ")" : ""), dot: "is-waiting" };
    }
    if (state.speed > 0) return { label: "playing " + trim(state.speed) + "x", dot: "is-playing" };
    if (state.target_slot) return { label: "stepping", dot: "is-stepping" };
    return { label: "max speed", dot: "is-stepping" };
  }

  function trim(value) {
    return String(Math.round(value * 100) / 100);
  }

  function num(value) {
    return (value === undefined || value === null) ? "-" : value.toLocaleString("en-US");
  }

  function render() {
    var status = mode();
    el.dot.className = "replay-dot " + status.dot;

    if (!state) {
      el.summary.textContent = status.label;
      return;
    }

    var now = virtualNow();
    var slot = state.running && state.rate && !state.holding ? slotAt(now) : state.virtual_slot;

    el.summary.textContent = "epoch " + num(state.virtual_epoch) + " · " + status.label;

    el.slot.textContent = num(slot) +
      (state.target_slot ? " → " + num(state.target_slot) : "");
    el.epoch.textContent = num(state.virtual_epoch) +
      (state.upstream_epoch ? " of " + num(state.upstream_epoch) + " upstream" : "");
    el.time.textContent = now ? new Date(now).toISOString().replace("T", " ").replace(/\.\d+Z$/, "Z") : "-";
    el.head.textContent = num(state.head_slot) +
      (state.head_root ? "  " + state.head_root.slice(0, 10) + "…" : "");
    el.checkpoints.textContent = "justified " + num(state.justified_epoch) +
      " · finalized " + num(state.finalized_epoch);
    el.execution.textContent = num(state.execution_block);

    var from = state.start_slot || 0;
    var to = state.upstream_slot || state.target_slot || slot;
    var span = to - from;
    el.fill.style.width = span > 0 ? Math.max(0, Math.min(100, ((slot - from) / span) * 100)) + "%" : "0";

    if (state.target_slot && span > 0) {
      el.marker.style.display = "block";
      el.marker.style.left = Math.max(0, Math.min(100, ((state.target_slot - from) / span) * 100)) + "%";
    } else {
      el.marker.style.display = "none";
    }

    el.rangeFrom.textContent = "from " + num(from);
    el.rangeTo.textContent = state.upstream_slot ? "chain head " + num(state.upstream_slot) : "";

    el.toggleRun.textContent = state.running ? "Stop" : "Start";
    el.step1.disabled = false;
    el.stepEpoch.disabled = false;

    el.source.textContent = state.upstream + (state.tracoor ? " (+tracoor)" : "");
  }

  function connect() {
    var stream = new EventSource(base + "/replay/events?topics=status");

    stream.addEventListener("open", function () {
      connected = true;
      render();
    });

    stream.addEventListener("status", function (event) {
      connected = true;
      try {
        apply(JSON.parse(event.data));
      } catch (err) {
        /* ignore a malformed frame and wait for the next one */
      }
    });

    stream.addEventListener("error", function () {
      connected = false;
      render();
      // EventSource reconnects on its own
    });
  }

  function start() {
    build();

    fetch(base + "/replay/status")
      .then(function (response) { return response.json(); })
      .then(function (status) {
        connected = true;
        apply(status);
      })
      .catch(function () {
        connected = false;
        render();
      });

    connect();

    // keep the interpolated clock and progress bar moving between status events
    window.setInterval(render, 500);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
