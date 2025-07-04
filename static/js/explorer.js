
(function() {
  window.addEventListener('DOMContentLoaded', function() {
    initControls();
    modalFixes();
    window.setInterval(updateTimers, 1000);
    initHeaderSearch();
  });
  var tooltipDict = {};
  var tooltipIdx = 1;
  window.explorer = {
    initControls: initControls,
    renderRecentTime: renderRecentTime,
    tooltipDict: tooltipDict,
    refreshPeerInfos: refreshPeerInfos,
    hexToDecimal: hexToDecimal,
    checkRefreshCooldown: checkRefreshCooldown,
  };

  function modalFixes() {
    // Fix bootstrap backdrop stacking when having multiple modals
    $(document).on('show.bs.modal', '.modal', function() {
      const offset = (10 * $('.modal:visible').length);
      const zIndex = 2000 + offset;
      $(this).css('z-index', zIndex);
      setTimeout(() => $('.modal-backdrop').not('.modal-stack').css('z-index', zIndex - offset - 1).addClass('modal-stack'));
    });
    // Fix bootstrap scrolling stacking when having multiple modals
    $(document).on('hidden.bs.modal', '.modal', function(){
      $('.modal:visible').length && $(document.body).addClass('modal-open')
    });
  }

  function initControls() {
    // init tooltips:
    // NOTE: `data-bs-toogle="tooltip"`` tooltips will also get cleaned up if their relevant element is removed from the DOM
    document.querySelectorAll('[data-bs-toggle="tooltip"]').forEach(initTooltip);
    cleanupTooltips();
    // NOTE: `data-toogle="tooltip"` tooltips will not get cleaned up if they are removed from the DOM
    $('[data-toggle="tooltip"]').tooltip()

    // init clipboard buttons
    var clipboard = new ClipboardJS('[data-clipboard-text], [data-clipboard-target]');
    clipboard.on("success", onClipboardSuccess);
    clipboard.on("error", onClipboardError);
  }

  function initTooltip(el) {
    if($(el).data("tooltip-init"))
      return;
    //console.log("init tooltip", el);
    var idx = tooltipIdx++;
    $(el).data("tooltip-init", idx).attr("data-tooltip-idx", idx.toString());
    $(el).tooltip();
    var tooltip = bootstrap.Tooltip.getInstance(el);
    tooltipDict[idx] = {
      element: el,
      tooltip: tooltip,
    };
  }

  function cleanupTooltips() {
    Object.keys(explorer.tooltipDict).forEach(function(idx) {
      var ref = explorer.tooltipDict[idx];
      if(document.body.contains(ref.element)) return;
      ref.tooltip.dispose();
      delete explorer.tooltipDict[idx];
    });
  }

  function onClipboardSuccess(e) {
    var title = e.trigger.getAttribute("data-bs-original-title");
    var tooltip = bootstrap.Tooltip.getInstance(e.trigger);
    tooltip.setContent({ '.tooltip-inner': 'Copied!' });
    tooltip.show();
    setTimeout(function () {
      tooltip.setContent({ '.tooltip-inner': title });
    }, 1000);
  }

  function onClipboardError(e) {
    var title = e.trigger.getAttribute("data-bs-original-title");
    var tooltip = bootstrap.Tooltip.getInstance(e.trigger);
    tooltip.setContent({ '.tooltip-inner': 'Failed to Copy!' });
    tooltip.show();
    setTimeout(function () {
      tooltip.setContent({ '.tooltip-inner': title });
    }, 1000);
  }

  function hexToDecimal(hexValue) {
    if (typeof hexValue !== 'string') return '';
    var cleanHex = hexValue.replace(/^0x/i, '');
    var decimal = parseInt(cleanHex, 16);
    return isNaN(decimal) ? '' : decimal.toString();
  }

  function checkRefreshCooldown() {
    var refreshButton = $('i[onclick="refreshPeerInfos()"]');
    
    if (refreshButton.length === 0) return; // Button not found on this page
    
    // Determine the client type based on current URL
    var clientType = window.location.pathname.includes('/clients/execution') ? 'execution' : 'consensus';
    
    fetch(`/clients/${clientType}/refresh/status`)
      .then(response => response.json())
      .then(data => {
        if (data.cooldown_active) {
          // Hide button during cooldown
          refreshButton.hide();
          
          var cooldownMsg = `Refresh cooldown active - ${data.remaining_seconds}s remaining`;
          if (data.online_clients) {
            if (data.total_cooldown === 60 && data.online_clients * 3 > 60) {
              cooldownMsg += ` (${data.online_clients} clients × 3s, capped at 60s)`;
            } else {
              cooldownMsg += ` (${data.online_clients} clients × 3s)`;
            }
          }
          refreshButton.attr('title', cooldownMsg);
          
          // Update countdown every second
          var countdown = setInterval(() => {
            fetch(`/clients/${clientType}/refresh/status`)
              .then(response => response.json())
              .then(statusData => {
                if (!statusData.cooldown_active) {
                  // Cooldown ended - show button
                  clearInterval(countdown);
                  refreshButton.show();
                  refreshButton.removeClass('disabled').css({
                    'opacity': '1',
                    'cursor': 'pointer',
                    'pointer-events': 'auto'
                  });
                  refreshButton.removeClass('fa-clock-o').addClass('fa-refresh');
                  refreshButton.attr('title', 'Refresh peer information');
                } else {
                  // Update remaining time
                  var cooldownMsg = `Refresh cooldown active - ${statusData.remaining_seconds}s remaining`;
                  if (statusData.online_clients) {
                    if (statusData.total_cooldown === 60 && statusData.online_clients * 3 > 60) {
                      cooldownMsg += ` (${statusData.online_clients} clients × 3s, capped at 60s)`;
                    } else {
                      cooldownMsg += ` (${statusData.online_clients} clients × 3s)`;
                    }
                  }
                  refreshButton.attr('title', cooldownMsg);
                }
              })
              .catch(() => {
                // On error, clear interval and reset button
                clearInterval(countdown);
                refreshButton.removeClass('disabled').css({
                  'opacity': '1',
                  'cursor': 'pointer',
                  'pointer-events': 'auto'
                });
                refreshButton.removeClass('fa-clock-o').addClass('fa-refresh');
                refreshButton.attr('title', 'Refresh peer information');
              });
          }, 1000);
        } else {
          // Button is available - show it
          refreshButton.show();
          refreshButton.removeClass('disabled').css({
            'opacity': '1',
            'cursor': 'pointer',
            'pointer-events': 'auto'
          });
          refreshButton.removeClass('fa-clock-o').addClass('fa-refresh');
          refreshButton.attr('title', 'Refresh peer information');
        }
      })
      .catch(error => {
        // On error, assume button is available - show it
        console.warn('Failed to check refresh cooldown status:', error);
        refreshButton.show();
        refreshButton.removeClass('disabled').css({
          'opacity': '1',
          'cursor': 'pointer',
          'pointer-events': 'auto'
        });
        refreshButton.removeClass('fa-clock-o').addClass('fa-refresh');
        refreshButton.attr('title', 'Refresh peer information');
      });
  }

  function updateTimers() {
    var timerEls = document.querySelectorAll("[data-timer]");
    timerEls.forEach(function(timerEl) {
      var time = timerEl.getAttribute("data-timer");
      var textEls = Array.prototype.filter.call(timerEl.querySelectorAll("*"), function(el) { return el.firstChild && el.firstChild.nodeType === 3 });
      var textEl = textEls.length ? textEls[0] : timerEl;

      textEl.innerText = renderRecentTime(time);
    });
  }

  function renderRecentTime(time) {
    var duration = time - Math.floor(new Date().getTime() / 1000);
    var timeStr= "";
    var absDuration = Math.abs(duration);

    if (absDuration < 1) {
      return "now";
    } else if (absDuration < 60) {
      timeStr = absDuration + " sec."
    } else if (absDuration < 60*60) {
      timeStr = (Math.floor(absDuration / 60)) + " min."
    } else if (absDuration < 24*60*60) {
      timeStr = (Math.floor(absDuration / (60 * 60))) + " hr."
    } else {
      timeStr = (Math.floor(absDuration / (60 * 60 * 24))) + " day."
    }
    if (duration < 0) {
      return timeStr + " ago";
    } else {
      return "in " + timeStr;
    }
  }

  function initHeaderSearch() {
    var searchEl = jQuery("#explorer-search");
    let requestNum = 9

    var prepareQueryFn = function(query, settings) {
      settings.url += encodeURIComponent(query);
      return settings;
    }

    var bhSlots = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.slot
      },
      remote: {
        url: "/search/slots?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhExecBlocks = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.slot
      },
      remote: {
        url: "/search/execblocks?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhEpochs = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.epoch
      },
      remote: {
        url: "/search/epochs?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhGraffiti = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.graffiti
      },
      remote: {
        url: "/search/graffiti?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhValNames = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.name
      },
      remote: {
        url: "/search/valname?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhValidators = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.index
      },
      remote: {
        url: "/search/validator?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });


    searchEl.typeahead(
      {
        minLength: 1,
        highlight: true,
        hint: false,
        autoselect: false,
      },
      {
        limit: 5,
        name: "slot",
        source: bhSlots,
        display: "root",
        templates: {
          header: '<h3 class="h5">Slots:</h3>',
          suggestion: function (data) {
            var status = "";
            if (data.orphaned) {
              status = `<span class="search-cell"><span class="badge rounded-pill text-bg-info">Orphaned</span></span>`;
            }
            return `<div class="text-monospace"><div class="search-table"><span class="search-cell">${data.slot}:</span><span class="search-cell search-truncate">${data.root}</span>${status}</div></div>`;
          },
        },
      },
      {
        limit: 5,
        name: "execblocks",
        source: bhExecBlocks,
        display: "root",
        templates: {
          header: '<h3 class="h5">Slots (by execution block):</h3>',
          suggestion: function (data) {
            var status = "";
            if (data.orphaned) {
              status = `<span class="search-cell"><span class="badge rounded-pill text-bg-info">Orphaned</span></span>`;
            }
            return `<div class="text-monospace"><div class="search-table"><span class="search-cell">${data.slot}:</span><span class="search-cell search-truncate"><nobr>Block ${data.exec_number} / ${data.exec_hash}</nobr></span>${status}</div></div>`;
          },
        },
      },
      {
        limit: 5,
        name: "name",
        source: bhValNames,
        display: "name",
        templates: {
          header: '<h3 class="h5">Slots (by validator name):</h3>',
          suggestion: function (data) {
            return `<div class="text-monospace" style="display:flex"><div class="text-truncate" style="flex:1 1 auto;">${data.name}</div><div style="max-width:fit-content;white-space:nowrap;">${data.count}</div></div>`
          },
        },
      },
      {
        limit: 5,
        name: "epoch",
        source: bhEpochs,
        display: "epoch",
        templates: {
          header: '<h3 class="h5">Epochs:</h3>',
          suggestion: function (data) {
            return `<div class="text-monospace">${data.epoch}</div>`
          },
        },
      },
      {
        limit: 5,
        name: "graffiti",
        source: bhGraffiti,
        display: "graffiti",
        templates: {
          header: '<h3 class="h5">Blocks (by graffitis):</h3>',
          suggestion: function (data) {
            return `<div class="text-monospace" style="display:flex"><div class="text-truncate" style="flex:1 1 auto;">${data.graffiti}</div><div style="max-width:fit-content;white-space:nowrap;">${data.count}</div></div>`
          },
        },
      },
      {
        limit: 5,
        name: "validator",
        source: bhValidators,
        display: "index",
        templates: {
          header: '<h3 class="h5">Validators:</h3>',
          suggestion: function (data) {
            var nameDisplay = data.name ? `<span class="text-muted" style="white-space:nowrap"> (${data.name})</span>` : '';
            return `<div class="text-monospace"><div class="search-table"><span class="search-cell">${data.index}:</span><span class="search-cell search-truncate">${data.pubkey}</span>${nameDisplay}</div></div>`;
          },
        },
      }
    )

    searchEl.on("input", function (input) {
      $(".tt-suggestion").first().addClass("tt-cursor")
    })

    jQuery(".tt-menu").on("mouseenter", function () {
      $(".tt-suggestion").first().removeClass("tt-cursor")
    })

    jQuery(".tt-menu").on("mouseleave", function () {
      $(".tt-suggestion").first().addClass("tt-cursor")
    })

    searchEl.on("typeahead:select", function (ev, sug) {
      if (sug.root !== undefined) {
        if (sug.orphaned) {
          window.location = "/slot/" + sug.root
        } else {
          window.location = "/slot/" + sug.slot
        }
      } else if (sug.epoch !== undefined) {
        window.location = "/epoch/" + sug.epoch
      } else if (sug.graffiti !== undefined) {
        // sug.graffiti is html-escaped to prevent xss, we need to unescape it
        var el = document.createElement("textarea")
        el.innerHTML = sug.graffiti
        window.location = "/slots/filtered?f&f.orphaned=1&f.graffiti=" + encodeURIComponent(el.value)
      } else if (sug.pubkey !== undefined) {
        window.location = "/validator/" + sug.index
      } else if (sug.name !== undefined) {
          // sug.name is html-escaped to prevent xss, we need to unescape it
          var el = document.createElement("textarea")
          el.innerHTML = sug.name
          window.location = "/slots/filtered?f&f.missing=1&f.orphaned=1&f.pname=" + encodeURIComponent(el.value)
      } else {
        console.log("invalid typeahead-selection", sug)
      }
    })
  }

  function refreshPeerInfos() {
    var refreshButton = $('i[onclick="refreshPeerInfos()"]');
    
    // Check if button is disabled due to cooldown
    if (refreshButton.hasClass('disabled') || refreshButton.css('pointer-events') === 'none') {
      return; // Don't allow refresh during cooldown
    }
    
    // Disable button and show spinning icon
    refreshButton.addClass('disabled').css({
      'opacity': '0.7',
      'pointer-events': 'none'
    });
    refreshButton.removeClass('fa-refresh fa-clock-o').addClass('fa-refresh fa-spin');
    
    // Determine the client type based on current URL
    var clientType = window.location.pathname.includes('/clients/execution') ? 'execution' : 'consensus';
    
    // Call the refresh API
    fetch(`/clients/${clientType}/refresh`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
    })
    .then(response => response.json())
    .then(data => {
      if (data.success) {
        // Success - show success message briefly then reload
        refreshButton.removeClass('fa-spin fa-refresh').addClass('fa-check text-success');
        refreshButton.attr('title', `Successfully refreshed ${data.refreshed_clients} clients`);
        setTimeout(() => {
          window.location.reload();
        }, 1000);
      } else {
        // Error - show message and re-enable button or start cooldown
        refreshButton.removeClass('fa-spin').addClass('fa-refresh');
        
        if (data.message && data.message.includes('cooldown')) {
          // Handle cooldown - start checking cooldown status
          checkRefreshCooldown();
        } else {
          // Other error - re-enable button
          refreshButton.removeClass('disabled').css({
            'opacity': '1',
            'pointer-events': 'auto'
          });
          alert('Failed to refresh peer information: ' + (data.message || 'Unknown error'));
        }
      }
    })
    .catch(error => {
      // Network error - show message and re-enable button
      refreshButton.removeClass('fa-spin').addClass('fa-refresh');
      refreshButton.removeClass('disabled').css({
        'opacity': '1',
        'pointer-events': 'auto'
      });
      alert('Failed to refresh peer information: ' + error.message);
    });
  }
})()

window.refreshPeerInfos = function() {
  var refreshButton = $('i[onclick="refreshPeerInfos()"]');
  
  // Check if button is disabled due to cooldown
  if (refreshButton.hasClass('disabled') || refreshButton.css('pointer-events') === 'none') {
    return; // Don't allow refresh during cooldown
  }
  
  // Disable button and show spinning icon
  refreshButton.addClass('disabled').css({
    'opacity': '0.7',
    'pointer-events': 'none'
  });
  refreshButton.removeClass('fa-refresh fa-clock-o').addClass('fa-refresh fa-spin');
  
  // Determine the client type based on current URL
  var clientType = window.location.pathname.includes('/clients/execution') ? 'execution' : 'consensus';
  
  // Call the refresh API
  fetch(`/clients/${clientType}/refresh`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
  })
  .then(response => response.json())
  .then(data => {
    if (data.success) {
      // Success - show success message briefly then reload
      refreshButton.removeClass('fa-spin fa-refresh').addClass('fa-check text-success');
      refreshButton.attr('title', `Successfully refreshed ${data.refreshed_clients} clients`);
      setTimeout(() => {
        window.location.reload();
      }, 1000);
    } else {
      // Error - show message and re-enable button or start cooldown
      refreshButton.removeClass('fa-spin').addClass('fa-refresh');
      
      if (data.message && data.message.includes('cooldown')) {
        // Handle cooldown - start checking cooldown status
        window.explorer.checkRefreshCooldown();
      } else {
        // Other error - re-enable button
        refreshButton.removeClass('disabled').css({
          'opacity': '1',
          'pointer-events': 'auto'
        });
        alert('Failed to refresh peer information: ' + (data.message || 'Unknown error'));
      }
    }
  })
  .catch(error => {
    // Network error - show message and re-enable button
    refreshButton.removeClass('fa-spin').addClass('fa-refresh');
    refreshButton.removeClass('disabled').css({
      'opacity': '1',
      'pointer-events': 'auto'
    });
    alert('Failed to refresh peer information: ' + error.message);
  });
};
