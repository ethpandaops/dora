
(function() {
  window.addEventListener('DOMContentLoaded', function() {
    window.setInterval(scheduleLoop, 500);
    // Initialize countdown tooltips immediately on page load
    initCountdownTooltips();
  });

  var refreshInterval = 15000;
  var lastRefresh = new Date().getTime();
  var loopTimer = null;
  var isRefreshing = false;
  var viewModel = null;
  // Countdown tooltip variables and functions
  var activeCountdowns = new Map(); // Track active countdowns
  var delegationInitialized = false; // Track if delegation is set up

  function initCountdownTooltips() {
    // Only set up delegation once per page load
    if (delegationInitialized) {
      return;
    }
    
    // Use event delegation for dynamically created elements
    var container = document.getElementById('frontpage_container') || document.body;
    
    // Add event listeners using delegation
    container.addEventListener('mouseenter', handleMouseEnter, true);
    container.addEventListener('mouseleave', handleMouseLeave, true);
    
    delegationInitialized = true;
  }
  
  function handleMouseEnter(event) {
    var element = event.target;
    var type = null;
    
    if (element.classList.contains('genesis-time-hover')) {
      type = 'genesis';
    } else if (element.classList.contains('fork-hover')) {
      type = 'fork';
    }
    
    if (type) {
      setupTimeTooltip(element, type);
    }
  }
  
  function handleMouseLeave(event) {
    var element = event.target;
    
    if (element.classList.contains('genesis-time-hover') || element.classList.contains('fork-hover')) {
      cleanupTooltip(element);
    }
  }
  
  function cleanupTooltip(element) {
    var countdownData = activeCountdowns.get(element);
    if (countdownData) {
      clearInterval(countdownData.interval);
      activeCountdowns.delete(element);
      
      // Restore original tooltip content for forks
      if (element.classList.contains('fork-hover')) {
        var originalTitle = countdownData.originalTitle;
        if (originalTitle) {
          element.setAttribute('title', originalTitle);
          element.setAttribute('data-bs-original-title', originalTitle);
        }
      }
    }
  }

  var baseModel = {
    formatAddCommas: function(x) { return x; },
    unixtime: function(x) { return Math.floor(new Date(x).getTime() / 1000); },
    timestamp: function(x) {
      var d = new Date(x);
      var p = /^([0-9-]+)T([0-9:]+).[0-9]+Z$/.exec(d.toISOString());
      return p[1] + " " + p[2] + " +0000 UTC";
    },
    timestampUtc: function(x) {
      var d = new Date(x);
      var p = /^([0-9-]+)T([0-9:]+).[0-9]+Z$/.exec(d.toISOString());
      return p[1] + " " + p[2] + " UTC";
    },
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
    window.explorer.pageModel = viewModel = Object.assign({}, baseModel);
    var val;
    for(var prop in data) {
      val = data[prop];
      if(Array.isArray(val))
        viewModel[prop] = ko.observableArray(val);
      else
        viewModel[prop] = ko.observable(val);
    }
    bindView();
    
    // Initialize countdown tooltips after model is created
    setTimeout(function() {
      initCountdownTooltips();
    }, 100);
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

  
  function setupTimeTooltip(element, type) {
    // Avoid duplicate setups
    if (activeCountdowns.has(element)) {
      return;
    }
    
    var timestamp = null;
    var isActive = false;
    
    if (type === 'genesis') {
      // Genesis time logic
      var dataTimestamp = element.getAttribute('data-genesis-timestamp');
      if (dataTimestamp) {
        timestamp = parseInt(dataTimestamp);
      }
      
      // Try from aria-ethereum-date attribute
      if (!timestamp) {
        var ariaDate = element.getAttribute('aria-ethereum-date');
        if (ariaDate) {
          timestamp = parseInt(ariaDate);
        }
      }
      
      // Try from viewModel
      if (!timestamp && viewModel && viewModel.genesis_time) {
        var genesisTime = viewModel.genesis_time();
        if (genesisTime) {
          timestamp = Math.floor(new Date(genesisTime).getTime() / 1000);
        }
      }
    } else if (type === 'fork') {
      // Fork time logic - try attributes first
      var forkTimestamp = element.getAttribute('data-fork-timestamp');
      var forkActive = element.getAttribute('data-fork-active');
      
      if (forkTimestamp) {
        timestamp = parseInt(forkTimestamp);
        isActive = forkActive === 'true';
      } else if (viewModel && viewModel.forks) {
        // Fallback to viewModel data for dynamically created elements
        var forkName = element.textContent.trim();
        var forks = viewModel.forks();
        for (var i = 0; i < forks.length; i++) {
          if (forks[i].name === forkName) {
            timestamp = forks[i].time;
            isActive = forks[i].active;
            break;
          }
        }
      }
    }
    
    
    if (timestamp) {
      var now = Math.floor(Date.now() / 1000);
      var diff = timestamp - now;
      
      // Only show countdown if time is in the future (for genesis) or future and inactive (for forks)
      var shouldShowCountdown = false;
      if (type === 'genesis' && diff > 0) {
        shouldShowCountdown = true;
      } else if (type === 'fork' && !isActive && diff > 0) {
        shouldShowCountdown = true;
      }
      
      if (shouldShowCountdown) {
        // Store original title - try multiple sources
        var originalTitle = element.getAttribute('title') || 
                          element.getAttribute('data-bs-original-title') ||
                          element.getAttribute('data-bs-title');
        
        // If no title found, construct it from the data
        if (!originalTitle && type === 'fork') {
          var forkName = element.textContent.trim();
          var forks = viewModel && viewModel.forks ? viewModel.forks() : [];
          for (var i = 0; i < forks.length; i++) {
            if (forks[i].name === forkName) {
              var fork = forks[i];
              originalTitle = 'Epoch: ' + fork.epoch + 
                            (fork.version ? '<br>Fork Version: ' + baseModel.hexstr(fork.version) : '') +
                            '<br>Time: ' + baseModel.timestampUtc(new Date(fork.time * 1000));
              break;
            }
          }
        }
        
        // Update countdown every second
        function updateCountdown() {
          var currentTime = Math.floor(Date.now() / 1000);
          var remaining = timestamp - currentTime;
          
          if (remaining <= 0) {
            cleanupTooltip(element);
            return;
          }
          
          var days = Math.floor(remaining / 86400);
          var hours = Math.floor((remaining % 86400) / 3600);
          var minutes = Math.floor((remaining % 3600) / 60);
          var seconds = remaining % 60;
          
          var countdownText = '';
          if (days > 0) {
            countdownText = days + 'd ' + hours + 'h ' + minutes + 'm ' + seconds + 's';
          } else if (hours > 0) {
            countdownText = hours + 'h ' + minutes + 'm ' + seconds + 's';
          } else if (minutes > 0) {
            countdownText = minutes + 'm ' + seconds + 's';
          } else {
            countdownText = seconds + 's';
          }
          
          // Update the tooltip content with countdown
          var newTitle = originalTitle + '<br><strong>Countdown: ' + countdownText + '</strong>';
          element.setAttribute('title', newTitle);
          element.setAttribute('data-bs-original-title', newTitle);
          
          // Update any existing bootstrap tooltip
          var bootstrapTooltip = bootstrap?.Tooltip?.getInstance(element);
          if (bootstrapTooltip) {
            bootstrapTooltip.setContent({'.tooltip-inner': newTitle});
          }
        }
        
        updateCountdown();
        countdownInterval = setInterval(updateCountdown, 1000);
        
        // Store countdown data for cleanup
        activeCountdowns.set(element, {
          interval: countdownInterval,
          originalTitle: originalTitle
        });
      }
    }
  }

})()
