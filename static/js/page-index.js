
(function() {
  window.addEventListener('DOMContentLoaded', function() {
    window.setInterval(scheduleLoop, 500);
  });

  var refreshInterval = 15000;
  var lastRefresh = new Date().getTime();
  var loopTimer = null;
  var isRefreshing = false;
  var viewModel = null;
  var baseModel = {
    formatAddCommas: function(x) { return x; },
    unixtime: function(x) { return Math.floor(new Date(x).getTime() / 1000); },
    timestamp: function(x) { return x; },
    formatRecentTimeShort: function(x) { return window.explorer.renderRecentTime(Math.floor(new Date(x).getTime() / 1000)); },
    formatEth: function(x) { return formatFloat(x / 1000000000, 4); },
    formatFloat: function(x) { return formatFloat(x, 2); },
    formatValidator: function(idx, name) { return formatValidator(idx, name); },
    hexstr: function(x) { return "0x" + base64ToHex(x); },
  };

  function scheduleLoop() {
    if(loopTimer)
      return;
    var refreshTimeout = refreshInterval - ((new Date().getTime() - lastRefresh));
    if(refreshTimeout < 0)
      refreshTimeout = 0;
    else if(refreshTimeout > 1000)
      refreshTimeout -= Math.floor(refreshTimeout/1000)*1000;
    loopTimer = setTimeout(refreshLoop, refreshTimeout);
  }

  function refreshLoop() {
    loopTimer = null;
    var refreshTimeout = refreshInterval - ((new Date().getTime() - lastRefresh));
    if(refreshTimeout < 0)
      refreshTimeout = 0;
    document.getElementById("update_timer").innerText = "Next update in " + Math.ceil(refreshTimeout / 1000) + "s";

    if(refreshTimeout <= 0) {
      lastRefresh = new Date().getTime();
      refreshTimeout = refreshInterval;
      refresh();
    }
  }

  async function refresh() {
    if(isRefreshing)
      return;
    isRefreshing = true;

    try {
      var pageData = await $.get("/index/data");
      updateModel(pageData);

      //console.log(pageData)
      window.explorer.initControls()
    } finally {
      isRefreshing = false;
    }
  }

  function mergeDataArr(model, data) {
    model.removeAll();
    for(var i = 0; i < data.length; i++) {
      model.push(data[i]);
    }
  }

  function updateModel(data) {
    if(!viewModel)
      return createModel(data);
    for(var prop in data) {
      if(typeof viewModel[prop] == "function") {
        if(viewModel[prop] instanceof ko.observableArray)
          viewModel[prop](data[prop]||[]);
        else
          viewModel[prop](data[prop]);
      } else
        viewModel[prop] = data[prop];
    }
  }

  function createModel(data) {
    window.explorer.pageModel = viewModel = Object.create(baseModel);
    var val;
    for(var prop in data) {
      val = data[prop];
      if(Array.isArray(val))
        viewModel[prop] = ko.observableArray(val);
      else
        viewModel[prop] = ko.observable(val);
    }
    bindView();
  }

  function bindView() {
    var pageContainer = document.getElementById("frontpage_container");
    document.querySelectorAll(".template-tbody").forEach(function(tplTBody) {
      var rows = Array.prototype.slice.call(tplTBody.children);
      rows.forEach(function(el) {
        if(el.classList.contains("template-row")) {
          el.classList.remove("template-row");
        } else {
          el.parentElement.removeChild(el);
        }
      });

    });
    ko.applyBindings(viewModel, pageContainer);
  }

  function formatFloat(x, d) {
    var factor = Math.pow(10, d);
    return Math.floor(x * factor) / factor;
  }

  function escapeHtml(unsafe) {
    return unsafe
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#039;");
  }

  function formatValidator(idx, name) {
    var icon = "fa-male mr-2";
    if(idx >= 9223372036854775807n) {
      return `<span class="validator-label validator-index"><i class="fas ` + icon + `"></i> unknown</span>`;
    }
    if(name != "") {
      return `<span class="validator-label validator-name" data-bs-toggle="tooltip" data-bs-placement="top" data-bs-title="` + idx + `"><i class="fas ` + icon + `"></i> <a href="/validator/` + idx + `">` + escapeHtml(name) + `</a></span>`;
    }
    return `<span class="validator-label validator-index"><i class="fas ` + icon + `"></i> <a href="/validator/` + idx + `">` + idx + `</a></span>`
  }

  function base64ToHex(str) {
    const raw = atob(str);
    let result = '';
    for (let i = 0; i < raw.length; i++) {
      const hex = raw.charCodeAt(i).toString(16);
      result += (hex.length === 2 ? hex : '0' + hex);
    }
    return result.toLowerCase();
  }

})()
