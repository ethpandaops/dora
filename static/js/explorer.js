
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
    hexToDecimal: hexToDecimal,
    formatEnrValue: formatEnrValue,
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

  function hexToUtf8(hex) {
    if (!hex || typeof hex !== 'string') return hex;
    
    // Remove 0x prefix if present
    var cleanHex = hex.replace(/^0x/i, '');
    
    // Check if it's actually hex
    if (!/^[0-9a-fA-F]*$/.test(cleanHex)) return hex;
    
    try {
      var bytes = [];
      for (var i = 0; i < cleanHex.length; i += 2) {
        bytes.push(parseInt(cleanHex.substr(i, 2), 16));
      }
      
      // Convert bytes to UTF-8 string
      var decoded = new TextDecoder('utf-8').decode(new Uint8Array(bytes));
      
      // Check if it's printable ASCII or contains common characters
      var isPrintable = /^[\x20-\x7E\n\r\t]*$/.test(decoded);
      var hasAlphanumeric = /[a-zA-Z0-9]/.test(decoded);
      
      if (isPrintable && hasAlphanumeric) {
        return decoded;
      }
    } catch(e) {
      // Return original if decoding fails
      console.log('Hex decode error for', hex, ':', e);
    }
    
    return hex;
  }

  function decodeRlpString(hex) {
    if (!hex || typeof hex !== 'string') return hex;
    
    var cleanHex = hex.replace(/^0x/i, '');
    if (cleanHex.length < 4) return hex;
    
    try {
      var firstByte = parseInt(cleanHex.substr(0, 2), 16);
      
      // Special handling for lighthouse format: d88a[string]
      if (firstByte === 0xd8) {
        var secondByte = parseInt(cleanHex.substr(2, 2), 16);
        if (secondByte >= 0x80 && secondByte <= 0xb7) {
          // This looks like d8 + string_encoding
          var stringLength = secondByte - 0x80;
          var stringHex = cleanHex.substr(4, stringLength * 2);
          return hexToUtf8('0x' + stringHex);
        }
      }
      
      // Standard RLP list
      if (firstByte >= 0xc0 && firstByte <= 0xf7) {
        // Short list
        var listLength = firstByte - 0xc0;
        var listHex = cleanHex.substr(2);
        var items = [];
        var offset = 0;
        
        while (offset < listHex.length && items.length < 10) {
          var itemFirstByte = parseInt(listHex.substr(offset, 2), 16);
          if (itemFirstByte >= 0x80 && itemFirstByte <= 0xb7) {
            var itemLength = itemFirstByte - 0x80;
            var itemHex = listHex.substr(offset + 2, itemLength * 2);
            var itemText = hexToUtf8('0x' + itemHex);
            if (itemText !== '0x' + itemHex) {
              items.push(itemText);
            }
            offset += 2 + itemLength * 2;
          } else if (itemFirstByte <= 0x7f) {
            // Single byte
            items.push(String.fromCharCode(itemFirstByte));
            offset += 2;
          } else {
            break;
          }
        }
        
        if (items.length > 0) {
          return items.join(', ');
        }
      } else if (firstByte >= 0x80 && firstByte <= 0xb7) {
        // Single string
        var length = firstByte - 0x80;
        var stringHex = cleanHex.substr(2, length * 2);
        return hexToUtf8('0x' + stringHex);
      }
    } catch(e) {
      console.log('RLP decode error for', hex, ':', e);
    }
    
    return hexToUtf8(hex);
  }

  function formatEnrValue(key, value) {
    if (!value) return value;
    
    switch(key) {
      case 'client':
        // Try RLP decoding first, then regular hex decoding
        var decoded = decodeRlpString(value);
        if (decoded === value) {
          decoded = hexToUtf8(value);
        }
        
        
        // Try to parse as JSON if it looks like JSON
        try {
          if (decoded.startsWith('[') || decoded.startsWith('"')) {
            var parsed = JSON.parse(decoded);
            if (Array.isArray(parsed)) {
              return parsed.join(', ');
            }
            return parsed;
          }
        } catch(e) {
          // Return decoded string if JSON parsing fails
        }
        return decoded;
      case 'cgc':
        // Show both hex and decimal for cgc
        var decimal = hexToDecimal(value);
        return value + (decimal ? ' (' + decimal + ')' : '');
      case 'seq':
        // Convert sequence number to decimal
        return hexToDecimal(value) || value;
      case 'ip':
        // IP is already in readable format
        return value;
      case 'tcp':
      case 'udp':
      case 'quic':
        // Ports - convert to decimal
        return hexToDecimal(value) || value;
      case 'id':
        // ID field is usually readable
        return value;
      case 'eth2':
      case 'attnets':
      case 'syncnets':
        // These are hex bitmasks, keep as hex but maybe show some info
        return value;
      default:
        // Try to decode as UTF-8 for other fields
        var decoded = hexToUtf8(value);
        return decoded !== value ? decoded : value;
    }
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

})()
