/**
 * Chain Fork Tree Visualization
 * Shows canonical chain and forks as a proper tree structure
 * Each fork is a vertical line from base to head
 * Horizontal axis only for positioning without overlap
 */

let zoomLevel = 1.0;
const ZOOM_MIN = 0.1; // Allow zooming out more for overview
const ZOOM_MAX = 20.0; // Allow much higher zoom for detailed inspection
const ZOOM_STEP_BASE = 0.1;

// Zoom focus tracking
let currentMousePos = { viewportX: 0, viewportY: 0, documentX: 0, documentY: 0 };
let scrollContainer = null;
let previousZoomLevel = 1.0;

// Tooltip event queue system to prevent stuttering
let tooltipQueue = {
    showTimer: null,
    hideTimer: null,
    currentElement: null,
    isShowing: false,
    pendingShow: null
};

// Fork highlight event queue system to prevent stuttering
let highlightQueue = {
    highlightTimer: null,
    unhighlightTimer: null,
    currentForkId: null,
    isHighlighted: false,
    pendingHighlight: null
};

// Data loading and refresh management
let currentDiagramData = null;
let dataRefreshInterval = null;
let isLoadingData = false;
let lastDataLoadTime = 0;
const REFRESH_INTERVAL = 15000; // 15 seconds

// View state preservation
let viewState = {
    hideSingleBlockForks: false,
    selectedForkId: null,
    detailsPanelContent: null,
    scrollPosition: { x: 0, y: 0 }
};

document.addEventListener('DOMContentLoaded', function() {
    if (typeof window.chainDiagramData === 'undefined') {
        console.warn('Chain diagram data not found');
        return;
    }
    
    // Initialize UI event listeners
    initializeEventListeners();
    
    // Load initial data
    loadDiagramData(false, () => {
        // Start periodic refresh after initial load
        startDataRefresh();
    });
});

function initializeEventListeners() {
    // Add zoom event listeners
    const zoomInBtn = document.getElementById('zoomIn');
    const zoomOutBtn = document.getElementById('zoomOut');
    const zoomResetBtn = document.getElementById('zoomReset');
    
    if (zoomInBtn) zoomInBtn.addEventListener('click', () => zoom(1));
    if (zoomOutBtn) zoomOutBtn.addEventListener('click', () => zoom(-1));
    if (zoomResetBtn) zoomResetBtn.addEventListener('click', resetZoom);
    
    // Add hide single-block forks checkbox listener
    const hideCheckbox = document.getElementById('hideSingleBlockForks');
    if (hideCheckbox) {
        hideCheckbox.addEventListener('change', () => {
            viewState.hideSingleBlockForks = hideCheckbox.checked;
            // Save scroll position before filter re-render
            saveScrollPosition();
            renderChainForkTree();
            // Restore scroll position after filter re-render
            restoreScrollPosition();
        });
    }
    
    // Add navigation link interceptors for time frame changes
    interceptNavigationLinks();
    
    // Add window resize listener to update container height
    let resizeTimeout;
    window.addEventListener('resize', () => {
        clearTimeout(resizeTimeout);
        resizeTimeout = setTimeout(() => {
            // Save scroll position before resize re-render
            saveScrollPosition();
            renderChainForkTree();
            // Restore scroll position after resize re-render
            restoreScrollPosition();
        }, 250); // Debounce resize events
    });
}

function interceptNavigationLinks() {
    // Intercept pagination and time range links to handle via AJAX
    const navLinks = document.querySelectorAll('a[href*="/chain-forks"]');
    
    navLinks.forEach(link => {
        link.addEventListener('click', (e) => {
            e.preventDefault();
            
            // Clear existing data when changing time frame
            clearViewState();
            
            // Update browser URL without reload
            window.history.pushState(null, '', link.href);
            
            // Load new data (buildAjaxUrl will read from updated window.location)
            loadDiagramData(false, () => {
                // Restart refresh if at head
                if (isAtHead()) {
                    startDataRefresh();
                } else {
                    stopDataRefresh();
                }
            });
        });
    });
}

function clearViewState() {
    // Clear view state when changing time frames
    viewState.selectedForkId = null;
    viewState.detailsPanelContent = null;
    viewState.scrollPosition = { x: 0, y: 0 }; // Reset scroll position for new time frame
    
    // Clear details panel
    const panel = document.getElementById('forkDetailsPanel');
    if (panel) {
        panel.innerHTML = `
            <div class="details-placeholder">
                <i class="fas fa-mouse-pointer text-muted mb-2"></i>
                <p class="text-muted">Click on any fork point to see details</p>
            </div>
        `;
    }
    
    // Stop any existing refresh interval
    stopDataRefresh();
}

function stopDataRefresh() {
    if (dataRefreshInterval) {
        clearInterval(dataRefreshInterval);
        dataRefreshInterval = null;
    }
}

function buildAjaxUrl() {
    const currentUrl = new URL(window.location);
    
    // Build AJAX URL with same parameters as current page
    const ajaxUrl = new URL(currentUrl.pathname, currentUrl.origin);
    ajaxUrl.searchParams.set('ajax', '1');
    
    // Copy existing URL parameters (start, size) if they exist
    const startParam = currentUrl.searchParams.get('start');
    const sizeParam = currentUrl.searchParams.get('size');
    
    if (startParam) {
        ajaxUrl.searchParams.set('start', startParam);
    }
    if (sizeParam) {
        ajaxUrl.searchParams.set('size', sizeParam);
    }
    
    return ajaxUrl.toString();
}

function loadDiagramData(isRefresh = false, callback = null) {
    if (isLoadingData) {
        return;
    }
    
    isLoadingData = true;
    const startTime = Date.now();
    
    // Show loading indicator if this is initial load
    if (!isRefresh && !currentDiagramData) {
        showLoadingIndicator();
    }
    
    fetch(buildAjaxUrl())
        .then(response => {
            if (!response.ok) {
                throw new Error(`HTTP ${response.status}: ${response.statusText}`);
            }
            return response.json();
        })
        .then(data => {
            lastDataLoadTime = Date.now();
            
            // Check for API errors
            if (data.error) {
                hideLoadingIndicator();
                showApiError(data.error);
                return;
            }
            
            // Check if this is new data or the same data
            const isNewData = !currentDiagramData || 
                             currentDiagramData.start_slot !== data.start_slot ||
                             currentDiagramData.end_slot !== data.end_slot ||
                             JSON.stringify(currentDiagramData.diagram) !== JSON.stringify(data.diagram);
            
            if (isNewData || !isRefresh) {
                // Save scroll position before refresh
                if (isRefresh) {
                    saveScrollPosition();
                }
                
                currentDiagramData = data;
                renderChainForkTree();
                
                // Restore scroll position and details panel if we're refreshing
                if (isRefresh) {
                    restoreScrollPosition();
                    if (viewState.selectedForkId) {
                        restoreDetailsPanel();
                    }
                } else {
                    // Show overview for initial load or when changing time frames
                    // Use setTimeout to ensure DOM is ready
                    setTimeout(showOverview, 0);
                }
            }
            
            hideLoadingIndicator();
            
            if (callback) {
                callback(data);
            }
        })
        .catch(error => {
            console.error('Error loading diagram data:', error);
            hideLoadingIndicator();
            showErrorMessage('Failed to load diagram data: ' + error.message);
        })
        .finally(() => {
            isLoadingData = false;
        });
}

function startDataRefresh() {
    if (dataRefreshInterval) {
        clearInterval(dataRefreshInterval);
    }
    
    dataRefreshInterval = setInterval(() => {
        // Only refresh if we're at head (no specific start time selected)
        if (isAtHead()) {
            loadDiagramData(true);
        }
    }, REFRESH_INTERVAL);
}

function isAtHead() {
    // Check if we're viewing the head of the chain (no start parameter in URL)
    const urlParams = new URLSearchParams(window.location.search);
    return !urlParams.has('start') || urlParams.get('start') === '';
}

function showLoadingIndicator() {
    const container = document.getElementById('chainDiagram');
    if (container) {
        container.style.opacity = '0.5';
        container.style.pointerEvents = 'none';
    }
}

function hideLoadingIndicator() {
    const container = document.getElementById('chainDiagram');
    if (container) {
        container.style.opacity = '1';
        container.style.pointerEvents = 'auto';
    }
}

function showErrorMessage(message) {
    const container = document.getElementById('chainDiagram');
    if (container) {
        container.innerHTML = `
            <div class="d-flex align-items-center justify-content-center h-100">
                <div class="text-center">
                    <i class="fas fa-exclamation-triangle text-warning fa-3x mb-3"></i>
                    <h5>Error Loading Data</h5>
                    <p class="text-muted">${message}</p>
                    <button class="btn btn-primary" onclick="loadDiagramData(false)">
                        <i class="fas fa-redo"></i> Retry
                    </button>
                </div>
            </div>
        `;
    }
}

function showApiError(errorMessage) {
    const container = document.getElementById('chainDiagram');
    if (container) {
        container.innerHTML = `
            <div class="d-flex align-items-center justify-content-center h-100">
                <div class="text-center">
                    <i class="fas fa-server text-danger fa-3x mb-3"></i>
                    <h5>Server Error</h5>
                    <p class="text-muted">${errorMessage}</p>
                    <button class="btn btn-primary" onclick="loadDiagramData(false)">
                        <i class="fas fa-redo"></i> Retry
                    </button>
                </div>
            </div>
        `;
    }
}

function restoreDetailsPanel() {
    // Find the fork in current data and refresh details panel
    if (!viewState.selectedForkId || !currentDiagramData) {
        return;
    }
    
    const fork = findForkById(viewState.selectedForkId);
    if (fork) {
        showForkDetails(fork);
    }
}

function findForkById(forkId) {
    if (!currentDiagramData || !currentDiagramData.diagram || !currentDiagramData.diagram.forks) {
        return null;
    }
    
    return currentDiagramData.diagram.forks.find(f => f.forkId === forkId);
}

function saveScrollPosition() {
    if (scrollContainer) {
        viewState.scrollPosition = {
            x: scrollContainer.scrollLeft,
            y: scrollContainer.scrollTop
        };
    }
}

function restoreScrollPosition() {
    if (scrollContainer && viewState.scrollPosition) {
        // Use requestAnimationFrame to ensure the DOM is updated
        requestAnimationFrame(() => {
            scrollContainer.scrollLeft = viewState.scrollPosition.x;
            scrollContainer.scrollTop = viewState.scrollPosition.y;
        });
    }
}

function renderChainForkTree() {
    const container = document.getElementById('chainDiagram');
    if (!container) {
        console.warn('Chain diagram container not found, deferring render');
        return;
    }
    
    // Return early if no data loaded yet
    if (!currentDiagramData) {
        return;
    }
    
    // Set container height dynamically based on viewport
    // Use 70% of viewport height, with min 600px and max 1200px
    const viewportHeight = window.innerHeight;
    const dynamicHeight = Math.min(Math.max(viewportHeight * 0.7, 600), 1200);
    container.style.height = `${dynamicHeight}px`;
    
    const staticData = window.chainDiagramData;
    let diagram = currentDiagramData.diagram;
    
    // Parse diagram if it's a JSON string
    if (typeof diagram === 'string') {
        try {
            diagram = JSON.parse(diagram);
        } catch (e) {
            console.error('Failed to parse diagram data:', e);
            renderEmptyDiagram(container);
            return;
        }
    }
    
    
    if (!diagram || !diagram.forks) {
        renderEmptyDiagram(container);
        return;
    }
    
    // Normalize fork property names (convert snake_case to camelCase)
    diagram.forks = diagram.forks.map(fork => ({
        forkId: fork.fork_id !== undefined ? fork.fork_id : fork.forkId,
        baseSlot: fork.base_slot !== undefined ? fork.base_slot : fork.baseSlot,
        baseRoot: fork.base_root !== undefined ? fork.base_root : fork.baseRoot,
        leafSlot: fork.leaf_slot !== undefined ? fork.leaf_slot : fork.leafSlot,
        leafRoot: fork.leaf_root !== undefined ? fork.leaf_root : fork.leafRoot,
        headSlot: fork.head_slot !== undefined ? fork.head_slot : fork.headSlot,
        headRoot: fork.head_root !== undefined ? fork.head_root : fork.headRoot,
        length: fork.length,
        blockCount: fork.block_count !== undefined ? fork.block_count : (fork.blockCount || 0),
        participation: fork.participation,
        participationByEpoch: fork.participation_by_epoch !== undefined ? fork.participation_by_epoch : fork.participationByEpoch,
        position: fork.position,
        parentFork: fork.parent_fork !== undefined ? fork.parent_fork : (fork.parentFork || 0),
        isCanonical: fork.is_canonical !== undefined ? fork.is_canonical : (fork.isCanonical || false)
    }));
    
    // Use view state for single-block forks filter
    if (viewState.hideSingleBlockForks) {
        // Hide single-block forks and merge chains
        diagram.forks = hideAndMergeSingleBlockForks(diagram.forks);
    } else {
        // Just reassign positions (client handles all positioning now)
        diagram.forks = reassignForkPositions(diagram.forks);
    }
    
    
    if (diagram.forks.length === 0) {
        renderEmptyDiagram(container);
        return;
    }
    
    // Calculate dimensions
    const containerAvailableWidth = container.offsetWidth - 40;
    
    // Calculate epoch range first to determine required height
    const startEpoch = currentDiagramData.start_epoch;
    const endEpoch = currentDiagramData.end_epoch;
    const epochRange = endEpoch - startEpoch;
    
    // Ensure each 5-epoch block is at least 50px (zoom applied later)
    const minHeightPer5Epochs = 50;
    const epochBlocks = Math.ceil(epochRange / 5);
    const minRequiredHeight = epochBlocks * minHeightPer5Epochs;
    
    // Use the larger of our minimum requirement or the default height (800px minimum)
    const baseContainerHeight = Math.max(800, minRequiredHeight + 80); // +80 for margins, 800px minimum
    const containerHeight = baseContainerHeight * zoomLevel;
    
    // Calculate required width based on number of forks
    const forkSpacing = 80; // Same as in getTreeX function, keep constant
    const maxPosition = Math.max(...diagram.forks.map(f => f.position || 0));
    const margin = { top: 40, right: 80, bottom: 40, left: 120 };
    const minRequiredWidth = margin.left + 40 + (maxPosition * forkSpacing) + margin.right + 100; // Extra padding
    
    // Use the larger of container width or required width
    const containerWidth = Math.max(containerAvailableWidth, minRequiredWidth);
    
    // Create SVG
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('width', containerWidth);
    svg.setAttribute('height', containerHeight);
    svg.setAttribute('viewBox', `0 0 ${containerWidth} ${containerHeight}`);
    svg.style.background = 'transparent';
    
    // Layout parameters
    const drawingWidth = containerWidth - margin.left - margin.right;
    const drawingHeight = containerHeight - margin.top - margin.bottom;
    
    // Calculate base time scale (zoom will be applied in getEpochY)
    const baseDrawingHeight = baseContainerHeight - margin.top - margin.bottom;
    const timeScale = epochRange > 0 ? baseDrawingHeight / epochRange : 1;
    
    
    
    // Helper functions
    function getEpochY(epoch) {
        // Head (newest) at top, older epochs below  
        const y = margin.top + ((endEpoch - epoch) * timeScale * zoomLevel);
        return y;
    }
    
    // Convert slot to epoch for Y positioning
    function getSlotY(slot) {
        const slotsPerEpoch = (staticData.specs && staticData.specs.slots_per_epoch) ? staticData.specs.slots_per_epoch : 32; // Use static specs data
        const epoch = Math.floor(slot / slotsPerEpoch);
        const y = getEpochY(epoch);
        
        
        return y;
    }
    
    function getTreeX(position) {
        // Git-style positioning: canonical on left, forks to the right
        const forkSpacing = 80; // Keep horizontal spacing constant - don't scale with zoom
        return margin.left + 40 + (position * forkSpacing); // Start from left margin + padding
    }
    
    function getParticipationColor(participation) {
        // Clamp participation to 0-1 range
        participation = Math.max(0, Math.min(1, participation));
        
        let r, g, b;
        
        if (participation >= 0.66) {
            // Green to dark green (66% to 100%)
            const t = (participation - 0.66) / 0.34; // 0 to 1
            // From bright green (0, 255, 0) to dark green (0, 128, 0)
            r = 0;
            g = Math.round(255 - 127 * t); // 255 to 128
            b = 0;
        } else if (participation >= 0.5) {
            // Yellow to green (50% to 66%)
            const t = (participation - 0.5) / 0.16; // 0 to 1
            r = Math.round(255 * (1 - t)); // 255 to 0
            g = 255; // Keep green at max
            b = 0;
        } else if (participation >= 0.3) {
            // Orange to yellow (30% to 50%)
            const t = (participation - 0.3) / 0.2; // 0 to 1
            r = 255;
            g = Math.round(165 + 90 * t); // 165 (orange) to 255 (yellow)
            b = 0;
        } else if (participation >= 0.1) {
            // Red to orange (10% to 30%)
            const t = (participation - 0.1) / 0.2; // 0 to 1
            r = 255;
            g = Math.round(165 * t); // 0 to 165 (orange)
            b = 0;
        } else if (participation > 0) {
            // Gray to red (0% to 10%)
            const t = participation / 0.1; // 0 to 1
            // From gray (128, 128, 128) to red (255, 0, 0)
            r = Math.round(128 + 127 * t); // 128 to 255
            g = Math.round(128 * (1 - t)); // 128 to 0
            b = Math.round(128 * (1 - t)); // 128 to 0
        } else {
            // Exactly 0% - gray
            r = 128;
            g = 128;
            b = 128;
        }
        
        return `rgb(${r}, ${g}, ${b})`;
    }
    
    
    // Draw time grid (horizontal lines) - showing every 5 epochs
    const epochStep = 5;
    // Start from the nearest multiple of epochStep at or before startEpoch
    const firstGridEpoch = Math.floor(startEpoch / epochStep) * epochStep;
    for (let epoch = firstGridEpoch; epoch <= endEpoch; epoch += epochStep) {
        const y = getEpochY(epoch);
        
        // Grid line
        const gridLine = document.createElementNS('http://www.w3.org/2000/svg', 'line');
        gridLine.setAttribute('x1', margin.left - 20);
        gridLine.setAttribute('y1', y);
        gridLine.setAttribute('x2', containerWidth - margin.right);
        gridLine.setAttribute('y2', y);
        gridLine.setAttribute('class', 'canonical-timeline');
        svg.appendChild(gridLine);
        
        // Epoch label
        const epochLabel = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        epochLabel.setAttribute('x', margin.left - 25);
        epochLabel.setAttribute('y', y + 4);
        epochLabel.setAttribute('text-anchor', 'end');
        epochLabel.setAttribute('class', 'slot-label'); // Keep same CSS class
        epochLabel.textContent = `E${epoch.toLocaleString()}`;
        svg.appendChild(epochLabel);
    }
    
    // Draw finality checkpoint line
    const finalitySlot = currentDiagramData.finality_slot;
    if (finalitySlot) {
        const slotsPerEpoch = (staticData.specs && staticData.specs.slots_per_epoch) ? staticData.specs.slots_per_epoch : 32;
        const finalityEpoch = Math.floor(finalitySlot / slotsPerEpoch);
        if (finalityEpoch >= startEpoch && finalityEpoch <= endEpoch) {
            const finalityY = getEpochY(finalityEpoch);
            
            // Finality checkpoint line
            const finalityLine = document.createElementNS('http://www.w3.org/2000/svg', 'line');
            finalityLine.setAttribute('x1', margin.left - 20);
            finalityLine.setAttribute('y1', finalityY);
            finalityLine.setAttribute('x2', containerWidth - margin.right);
            finalityLine.setAttribute('y2', finalityY);
            finalityLine.setAttribute('stroke', '#007bff');
            finalityLine.setAttribute('stroke-width', '2');
            finalityLine.setAttribute('stroke-dasharray', '5,5');
            finalityLine.setAttribute('class', 'finality-checkpoint');
            svg.appendChild(finalityLine);
            
            // Finality label
            const finalityLabel = document.createElementNS('http://www.w3.org/2000/svg', 'text');
            finalityLabel.setAttribute('x', containerWidth - margin.right + 5);
            finalityLabel.setAttribute('y', finalityY + 4);
            finalityLabel.setAttribute('class', 'finality-label');
            finalityLabel.textContent = `Finalized (E${finalityEpoch})`;
            finalityLabel.style.fill = '#007bff';
            finalityLabel.style.fontSize = '11px';
            finalityLabel.style.fontWeight = '600';
            svg.appendChild(finalityLabel);
        }
    }
    
    // Create a lookup for fork positions
    const forkPositions = {};
    const forkHeadY = {}; // Store actual head Y positions after adjustment
    diagram.forks.forEach(fork => {
        forkPositions[fork.forkId] = getTreeX(fork.position);
    });
    
    
    // Find the highest canonical fork (the one with the latest head slot at position 0)
    let highestCanonicalFork = null;
    diagram.forks.forEach(fork => {
        if (fork.position === 0) {
            if (!highestCanonicalFork || fork.headSlot > highestCanonicalFork.headSlot) {
                highestCanonicalFork = fork;
            }
        }
    });
    
    // First pass: Draw all fork lines and branch connections
    // Second pass: Draw all bubbles and labels (so they appear on top)
    
    // First pass: Draw lines
    diagram.forks.forEach((fork, index) => {
        // Validate fork data
        if (!fork || typeof fork.baseSlot !== 'number' || typeof fork.headSlot !== 'number') {
            console.warn('Invalid fork data:', fork);
            return;
        }
        
        const baseY = getSlotY(fork.baseSlot);
        const headY = getSlotY(fork.headSlot);
        const forkX = getTreeX(fork.position);
        
        
        
        // Use actual data positions - no artificial adjustments!
        let adjustedHeadY = headY;
        
        // Validate calculated positions
        if (isNaN(baseY) || isNaN(adjustedHeadY) || isNaN(forkX)) {
            console.warn('Invalid position calculated for fork:', fork, { baseY, headY: adjustedHeadY, forkX });
            return;
        }
        
        // Store the actual head Y position for this fork
        forkHeadY[fork.forkId] = headY;
        
        // Calculate participation color for elements outside the main fork line
        const participationColor = getParticipationColor(fork.participation);
        
        // Draw branching connection FIRST (horizontal line from parent to fork start)
        // This ensures horizontal lines appear behind vertical fork lines
        if (forkPositions[fork.parentFork] !== undefined && fork.forkId !== fork.parentFork) {
            const parentX = forkPositions[fork.parentFork];
            const parentFork = diagram.forks.find(f => f.forkId === fork.parentFork);
            
            // Only draw branching connection if parent and child are at different X positions
            // Canonical chain forks at the same position don't need horizontal branch lines
            if (parentX !== forkX) {
                // Determine the Y position for the branch point
                let branchY = baseY;
                
                // If the parent fork ends at or near this fork's base slot, and the parent has an adjusted head position,
                // use the parent's actual head Y position for the branch point
                if (parentFork && Math.abs(parentFork.headSlot - fork.baseSlot) <= 1 && forkHeadY[parentFork.forkId] !== undefined) {
                    branchY = forkHeadY[parentFork.forkId];
                }
                // Draw horizontal branching line (always gray, behind fork lines)
                const branchLine = document.createElementNS('http://www.w3.org/2000/svg', 'line');
                // For canonical chain (fork 0), start directly at the line (no bullet offset)
                // For other forks, offset by bullet radius (6px)
                const startX = fork.parentFork === 0 ? parentX : parentX + 6;
                branchLine.setAttribute('x1', startX);
                branchLine.setAttribute('y1', branchY);
                branchLine.setAttribute('x2', forkX);
                branchLine.setAttribute('y2', branchY);
                branchLine.setAttribute('class', 'fork-branch-line');
                branchLine.setAttribute('stroke', '#6c757d'); // Always gray
                branchLine.setAttribute('stroke-width', '2');
                branchLine.setAttribute('data-fork-id', fork.forkId);
                // Removed stroke-dasharray to make branching lines solid
                branchLine.style.cursor = 'pointer';
                branchLine.style.opacity = '0.6';
                svg.appendChild(branchLine);
                
                // Add wider invisible hit area for easier hovering
                const branchHitArea = document.createElementNS('http://www.w3.org/2000/svg', 'line');
                branchHitArea.setAttribute('x1', startX);
                branchHitArea.setAttribute('y1', branchY);
                branchHitArea.setAttribute('x2', forkX);
                branchHitArea.setAttribute('y2', branchY);
                branchHitArea.setAttribute('stroke', 'transparent');
                branchHitArea.setAttribute('stroke-width', '8'); // Wider hit area for horizontal lines
                branchHitArea.setAttribute('data-fork-id', fork.forkId);
                branchHitArea.setAttribute('data-hit-area', 'true'); // Mark as hit area to exclude from highlighting
                branchHitArea.style.cursor = 'pointer';
                svg.appendChild(branchHitArea); // Add after original line so it's on top
                
                // Removed branch point circle - only showing head bubbles
            }
        }

        // Draw fork timeline SECOND (vertical lines appear in front of horizontal lines)
        let forkElements = [];
        
        if (fork.baseSlot !== fork.headSlot) {
            // Determine the starting Y position for the fork line
            let startY = baseY;
            
            // If this fork continues from a parent (either at same or different position), 
            // and the parent ends at this fork's base, start drawing from the parent's actual head Y position
            const parentFork = diagram.forks.find(f => f.forkId === fork.parentFork);
            if (parentFork && 
                Math.abs(parentFork.headSlot - fork.baseSlot) <= 1 && 
                forkHeadY[parentFork.forkId] !== undefined) {
                startY = forkHeadY[parentFork.forkId];
            }
            
            // Check if we have epoch-based participation data
            if (fork.participationByEpoch && Array.isArray(fork.participationByEpoch) && fork.participationByEpoch.length > 0) {
                // Draw segments with different colors for each epoch
                forkElements = drawForkWithParticipationGradient(fork, forkX, startY, adjustedHeadY, svg, getSlotY);
            } else {
                // Fall back to single-color line (no epoch data or empty array)
                const forkLine = document.createElementNS('http://www.w3.org/2000/svg', 'line');
                forkLine.setAttribute('x1', forkX);
                forkLine.setAttribute('y1', startY);
                forkLine.setAttribute('x2', forkX);
                forkLine.setAttribute('y2', adjustedHeadY);
                forkLine.setAttribute('class', 'fork-line');
                forkLine.setAttribute('stroke', participationColor);
                forkLine.setAttribute('data-fork-id', fork.forkId);
                forkLine.setAttribute('stroke-width', '3');
                forkLine.style.cursor = 'pointer';
                svg.appendChild(forkLine);
                
                // Add wider invisible hit area for easier hovering
                const hitArea = document.createElementNS('http://www.w3.org/2000/svg', 'line');
                hitArea.setAttribute('x1', forkX);
                hitArea.setAttribute('y1', startY);
                hitArea.setAttribute('x2', forkX);
                hitArea.setAttribute('y2', adjustedHeadY);
                hitArea.setAttribute('stroke', 'transparent');
                hitArea.setAttribute('stroke-width', '12'); // Much wider hit area
                hitArea.setAttribute('data-fork-id', fork.forkId);
                hitArea.setAttribute('data-hit-area', 'true'); // Mark as hit area to exclude from highlighting
                hitArea.style.cursor = 'pointer';
                svg.appendChild(hitArea); // Add after original line so it's on top
                
                forkElements.push(forkLine, hitArea);
            }
        }
    });
    
    // Second pass: Draw all bubbles, labels, and add interactivity
    diagram.forks.forEach((fork) => {
        if (!fork || typeof fork.baseSlot !== 'number' || typeof fork.headSlot !== 'number') {
            return;
        }
        
        const forkX = getTreeX(fork.position);
        const baseY = getSlotY(fork.baseSlot);
        const headY = getSlotY(fork.headSlot);
        
        // Use actual head Y position - no adjustments
        let adjustedHeadY = headY;
        
        const participationColor = getParticipationColor(fork.participation);
        
        // Fork head point (current head of this fork)
        const headPoint = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
        headPoint.setAttribute('cx', forkX);
        headPoint.setAttribute('cy', adjustedHeadY);
        headPoint.setAttribute('r', '6'); // Same size for all forks
        headPoint.setAttribute('fill', participationColor);
        headPoint.setAttribute('stroke', 'var(--fork-bg-primary)');
        headPoint.setAttribute('stroke-width', '2');
        headPoint.setAttribute('class', 'fork-point participation-indicator');
        headPoint.setAttribute('data-fork-id', fork.forkId);
        headPoint.style.cursor = 'pointer';
        svg.appendChild(headPoint);
        
        // Fork ID label
        const forkLabel = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        forkLabel.setAttribute('x', forkX + 10);
        forkLabel.setAttribute('y', adjustedHeadY - 4);
        forkLabel.setAttribute('class', 'fork-label');
        if (fork.position === 0 && highestCanonicalFork && fork.forkId === highestCanonicalFork.forkId) {
            // Only show "Canonical" label for the highest canonical fork
            forkLabel.textContent = 'Canonical';
        } else {
            forkLabel.textContent = `Fork ${fork.forkId}`;
        }
        svg.appendChild(forkLabel);
        
        // Block count label (only show for forks with 32+ slots)
        const forkSlotLength = fork.headSlot - fork.baseSlot;
        if (forkSlotLength >= 32) {
            const blockCountLabel = document.createElementNS('http://www.w3.org/2000/svg', 'text');
            blockCountLabel.setAttribute('x', forkX + 12);
            blockCountLabel.setAttribute('y', (baseY + adjustedHeadY) / 2);
            blockCountLabel.setAttribute('class', 'block-count-label');
            blockCountLabel.textContent = `${fork.blockCount || fork.block_count || 0} blocks`;
            svg.appendChild(blockCountLabel);
        }
        
    });
    
    // Third pass: Add event listeners to ALL elements after everything is drawn
    diagram.forks.forEach((fork) => {
        if (!fork || typeof fork.baseSlot !== 'number' || typeof fork.headSlot !== 'number') {
            return;
        }
        
        const forkElements = svg.querySelectorAll(`[data-fork-id="${fork.forkId}"]`);
        
        forkElements.forEach(el => {
            if (el) {
                el.addEventListener('click', () => showForkDetails(fork));
                el.addEventListener('mouseenter', () => queueHighlightFork(fork.forkId));
                el.addEventListener('mouseleave', () => queueUnhighlightFork(fork.forkId));
            }
        });
    });
    
    
    // Clear container and create scroll wrapper
    container.innerHTML = '';
    
    // Create scroll container
    scrollContainer = document.createElement('div');
    scrollContainer.className = 'diagram-scroll-container';
    scrollContainer.appendChild(svg);
    
    // Add mouse tracking for zoom focus
    scrollContainer.addEventListener('mousemove', (event) => {
        const rect = scrollContainer.getBoundingClientRect();
        // Store both viewport coordinates and document coordinates for better precision
        currentMousePos.viewportX = event.clientX - rect.left;
        currentMousePos.viewportY = event.clientY - rect.top;
        currentMousePos.documentX = currentMousePos.viewportX + scrollContainer.scrollLeft;
        currentMousePos.documentY = currentMousePos.viewportY + scrollContainer.scrollTop;
    });
    
    // Add mouse wheel zoom to scroll container
    scrollContainer.addEventListener('wheel', handleWheelZoom, { passive: false });
    
    container.appendChild(scrollContainer);
    
    // Show initial overview in details panel
    showOverview();
}

function highlightFork(forkId) {
    const elements = document.querySelectorAll(`[data-fork-id="${forkId}"]`);
    
    elements.forEach(el => {
        // Skip hit areas - don't move or modify them
        if (el.getAttribute('data-hit-area') === 'true') {
            return;
        }
        
        // Move element to end of parent to increase z-index
        el.parentNode.appendChild(el);
        
        if (el.tagName === 'line') {
            // Simple approach: just override with style, restore by removing style
            el.setAttribute('data-hover-active', 'true');
            el.style.strokeWidth = '5';
        } else if (el.tagName === 'circle') {
            // Store original radius and increase
            const currentRadius = el.getAttribute('r') || '6';
            el.setAttribute('data-original-radius', currentRadius);
            el.setAttribute('r', '8');
        }
    });
}

function unhighlightFork(forkId) {
    const elements = document.querySelectorAll(`[data-fork-id="${forkId}"]`);
    
    elements.forEach(el => {
        // Skip hit areas - don't modify them
        if (el.getAttribute('data-hit-area') === 'true') {
            return;
        }
        
        if (el.tagName === 'line') {
            // Simple restore: just remove the style override
            if (el.getAttribute('data-hover-active')) {
                el.style.removeProperty('stroke-width');
                el.removeAttribute('data-hover-active');
            }
        } else if (el.tagName === 'circle') {
            // Restore original radius
            const originalRadius = el.getAttribute('data-original-radius');
            if (originalRadius) {
                el.setAttribute('r', originalRadius);
                el.removeAttribute('data-original-radius');
            }
        }
    });
}

// Debounced highlight functions to prevent stuttering
function queueHighlightFork(forkId) {
    // Clear any pending unhighlight timer
    if (highlightQueue.unhighlightTimer) {
        clearTimeout(highlightQueue.unhighlightTimer);
        highlightQueue.unhighlightTimer = null;
    }
    
    // If already highlighting the same fork, do nothing
    if (highlightQueue.isHighlighted && highlightQueue.currentForkId === forkId) {
        return;
    }
    
    // Clear any pending highlight timer
    if (highlightQueue.highlightTimer) {
        clearTimeout(highlightQueue.highlightTimer);
    }
    
    // Store pending highlight data
    highlightQueue.pendingHighlight = forkId;
    highlightQueue.currentForkId = forkId;
    
    // Set timer to highlight after 30ms (shorter than tooltip for responsiveness)
    highlightQueue.highlightTimer = setTimeout(() => {
        // Only highlight if we're still hovering the same fork
        if (highlightQueue.currentForkId === forkId && highlightQueue.pendingHighlight === forkId) {
            highlightFork(forkId);
            highlightQueue.isHighlighted = true;
            highlightQueue.pendingHighlight = null;
        }
        highlightQueue.highlightTimer = null;
    }, 30);
}

function queueUnhighlightFork(forkId) {
    // Only process unhighlight if we were highlighting or about to highlight this fork
    if (highlightQueue.currentForkId !== forkId) {
        return;
    }
    
    // Clear any pending highlight timer
    if (highlightQueue.highlightTimer) {
        clearTimeout(highlightQueue.highlightTimer);
        highlightQueue.highlightTimer = null;
        highlightQueue.pendingHighlight = null;
    }
    
    // Only unhighlight if we're actually highlighting
    if (highlightQueue.isHighlighted) {
        // Set timer to unhighlight after a short delay to prevent flicker
        highlightQueue.unhighlightTimer = setTimeout(() => {
            unhighlightFork(forkId);
            highlightQueue.isHighlighted = false;
            highlightQueue.currentForkId = null;
            highlightQueue.unhighlightTimer = null;
        }, 10);
    } else {
        // Not highlighting, just reset
        highlightQueue.currentForkId = null;
    }
}

function showForkDetails(fork) {
    const panel = document.getElementById('forkDetailsPanel');
    
    // Update view state to preserve selection during refresh
    viewState.selectedForkId = fork.forkId;
    
    const participationPercent = (fork.participation * 100).toFixed(1); // Convert 0-1 to percentage
    const participationColor = getParticipationColor(fork.participation);
    
    // Handle cases where slot values might be undefined or NaN
    const baseSlot = fork.baseSlot || 0;
    const leafSlot = fork.leafSlot || 0;
    const headSlot = fork.headSlot || 0;
    const length = fork.length || 1;
    const forkId = fork.forkId || 0;
    const isCanonical = fork.position === 0; // Canonical chain is always at position 0
    
    // Check if this is a merged fork
    const mergedForkIds = fork.mergedForkIds;
    const isMerged = mergedForkIds && mergedForkIds.length > 1;
    
    panel.innerHTML = `
        <div class="fork-detail-card">
            <div class="fork-detail-header">
                <span class="fork-badge ${isCanonical ? 'canonical' : 'orphaned'}">
                    ${isCanonical ? 'Canonical Chain' : `Fork ${forkId}${isMerged ? ' (Merged)' : ''}`}
                </span>
                <strong>Chain Fork Details</strong>
            </div>
            
            <div class="fork-detail-grid">
                <span class="detail-label">Fork ID:</span>
                <span class="detail-value">${forkId}</span>
                
                ${isMerged ? `
                <span class="detail-label">Merged Fork IDs:</span>
                <span class="detail-value">${mergedForkIds.join(', ')}</span>
                ` : ''}
                
                <span class="detail-label">Base ${isCanonical ? 'Slot' : 'Block'}:</span>
                <span class="detail-value">
                    ${!fork.baseRoot ? 
                        `<a href="/slot/${baseSlot}">${baseSlot.toLocaleString()}</a>` :
                        `<a href="/slot/0x${bytesToHex(fork.baseRoot)}" title="0x${bytesToHex(fork.baseRoot)}">
                            ${baseSlot.toLocaleString()} (0x${bytesToHex(fork.baseRoot).substring(0, 8)}...)
                        </a>`
                    }
                </span>
                
                <span class="detail-label">First Diverging Block:</span>
                <span class="detail-value">
                    ${!fork.leafRoot ? 
                        `${leafSlot.toLocaleString()}` :
                        `<a href="/slot/0x${bytesToHex(fork.leafRoot)}" title="0x${bytesToHex(fork.leafRoot)}">
                            ${leafSlot.toLocaleString()} (0x${bytesToHex(fork.leafRoot).substring(0, 8)}...)
                        </a>`
                    }
                </span>
                
                <span class="detail-label">Head ${isCanonical ? 'Slot' : 'Block'}:</span>
                <span class="detail-value">
                    ${!fork.headRoot ? 
                        `<a href="/slot/${headSlot}">${headSlot.toLocaleString()}</a>` :
                        `<a href="/slot/0x${bytesToHex(fork.headRoot)}" title="0x${bytesToHex(fork.headRoot)}">
                            ${headSlot.toLocaleString()} (0x${bytesToHex(fork.headRoot).substring(0, 8)}...)
                        </a>`
                    }
                </span>
                
                <span class="detail-label">Slot Range:</span>
                <span class="detail-value">${length.toLocaleString()} slots</span>
                
                <span class="detail-label">Block Count:</span>
                <span class="detail-value">
                    ${(fork.blockCount || 0).toLocaleString()} blocks
                    ${forkId !== 0 ? `
                        <a href="/slots/filtered?f=1&f.missing=0&f.orphaned=1&f.fork=${isMerged ? mergedForkIds.join(',') : forkId}" 
                           class="text-decoration-none ms-2" 
                           title="View filtered slots for this fork"
                           style="font-size: 0.875rem; color: #6c757d;">
                            <i class="fas fa-eye"></i> View
                        </a>
                    ` : `
                        <a href="/slots/filtered" 
                           class="text-decoration-none ms-2" 
                           title="View all slots"
                           style="font-size: 0.875rem; color: #6c757d;">
                            <i class="fas fa-eye"></i> View
                        </a>
                    `}
                </span>
                
                <span class="detail-label">Duration:</span>
                <span class="detail-value">${(length * ((window.chainDiagramData.specs && window.chainDiagramData.specs.seconds_per_slot) ? window.chainDiagramData.specs.seconds_per_slot : 12)).toLocaleString()} seconds</span>
                
                <span class="detail-label">Parent Fork:</span>
                <span class="detail-value">${fork.parentFork === 0 ? 'Canonical Chain' : `Fork ${fork.parentFork}`}</span>
                
                <span class="detail-label">Participation:</span>
                <span class="detail-value">
                    ${fork.participation > 0 ? `
                        <div class="participation-bar">
                            <div class="participation-fill" style="width: ${participationPercent}%; background: ${participationColor}"></div>
                            <span class="participation-text">${participationPercent}%</span>
                        </div>
                    ` : 'No data available'}
                </span>
                
                ${fork.baseRoot ? `
                <span class="detail-label">Base Root:</span>
                <span class="detail-value" style="display: flex; align-items: center;">
                    <a href="/slot/0x${bytesToHex(fork.baseRoot)}" title="0x${bytesToHex(fork.baseRoot)}" style="flex: 1;">
                        0x${bytesToHex(fork.baseRoot).substring(0, 16)}...
                    </a>
                    <i class="fas fa-copy copy-button" onclick="copyToClipboard('0x${bytesToHex(fork.baseRoot)}')" style="flex-shrink: 0;"></i>
                </span>
                ` : ''}
                
                ${fork.leafRoot && fork.leafSlot !== fork.baseSlot ? `
                <span class="detail-label">First Fork Root:</span>
                <span class="detail-value" style="display: flex; align-items: center;">
                    <a href="/slot/0x${bytesToHex(fork.leafRoot)}" title="0x${bytesToHex(fork.leafRoot)}" style="flex: 1;">
                        0x${bytesToHex(fork.leafRoot).substring(0, 16)}...
                    </a>
                    <i class="fas fa-copy copy-button" onclick="copyToClipboard('0x${bytesToHex(fork.leafRoot)}')" style="flex-shrink: 0;"></i>
                </span>
                ` : ''}
                
                ${fork.headRoot ? `
                <span class="detail-label">Head Root:</span>
                <span class="detail-value" style="display: flex; align-items: center;">
                    <a href="/slot/0x${bytesToHex(fork.headRoot)}" title="0x${bytesToHex(fork.headRoot)}" style="flex: 1;">
                        0x${bytesToHex(fork.headRoot).substring(0, 16)}...
                    </a>
                    <i class="fas fa-copy copy-button" onclick="copyToClipboard('0x${bytesToHex(fork.headRoot)}')" style="flex-shrink: 0;"></i>
                </span>
                ` : ''}
            </div>
        </div>
    `;
}

function showOverview() {
    const panel = document.getElementById('forkDetailsPanel');
    
    if (!panel) {
        console.warn('Fork details panel not found');
        return;
    }
    
    if (!currentDiagramData) {
        panel.innerHTML = `
            <div class="details-placeholder">
                <i class="fas fa-mouse-pointer text-muted mb-2"></i>
                <p class="text-muted">Loading diagram data...</p>
            </div>
        `;
        return;
    }
    
    let diagram = currentDiagramData.diagram;
    
    if (typeof diagram === 'string') {
        diagram = JSON.parse(diagram);
    }
    
    if (!diagram || !diagram.forks || diagram.forks.length === 0) {
        panel.innerHTML = `
            <div class="details-placeholder">
                <i class="fas fa-check-circle text-success mb-2"></i>
                <p class="text-muted">No chain forks detected in this time range</p>
            </div>
        `;
        return;
    }
    
    const totalForks = diagram.forks.filter(f => (f.fork_id || f.forkId) !== 0).length;
    const avgParticipation = totalForks > 0 ? 
        diagram.forks.filter(f => (f.fork_id || f.forkId) !== 0)
                   .reduce((sum, f) => sum + (f.participation || 0), 0) / totalForks : 0; // Already 0-1
    const maxLength = totalForks > 0 ? 
        Math.max(...diagram.forks.filter(f => (f.fork_id || f.forkId) !== 0).map(f => f.length || 1)) : 0;
    const maxBlockCount = totalForks > 0 ?
        Math.max(...diagram.forks.filter(f => (f.fork_id || f.forkId) !== 0).map(f => f.blockCount || 0)) : 0;
    
    panel.innerHTML = `
        <div class="fork-detail-card">
            <div class="fork-detail-header">
                <strong>Fork Overview</strong>
            </div>
            
            <div class="fork-detail-grid">
                <span class="detail-label">Time Range:</span>
                <span class="detail-value">Epochs ${currentDiagramData.start_epoch || 0} - ${currentDiagramData.end_epoch || 0}</span>
                
                <span class="detail-label">Total Forks:</span>
                <span class="detail-value">${totalForks}</span>
                
                <span class="detail-label">Longest Fork:</span>
                <span class="detail-value">${maxLength} slots (${maxBlockCount} blocks)</span>
                
                <span class="detail-label">Avg Participation:</span>
                <span class="detail-value">
                    ${avgParticipation > 0 ? `
                        <div class="participation-bar">
                            <div class="participation-fill" style="width: ${(avgParticipation * 100).toFixed(1)}%; background: ${getParticipationColor(avgParticipation)}"></div>
                            <span class="participation-text">${(avgParticipation * 100).toFixed(1)}%</span>
                        </div>
                    ` : 'No data available'}
                </span>
            </div>
            
            <div style="margin-top: 16px; padding-top: 16px; border-top: 1px solid var(--fork-border-color); font-size: 12px; color: var(--fork-text-secondary);">
                <strong>Visualization:</strong><br>
                • Canonical chain flows vertically (newest at top)<br>
                • Forks branch diagonally based on divergence<br>
                • Colors indicate participation levels<br>
                • Click any fork for detailed information
            </div>
        </div>
        
        <div style="margin-top: 12px; font-size: 12px; color: var(--fork-text-secondary); text-align: center;">
            Click on any fork point or line for details
        </div>
    `;
}

function renderEmptyDiagram(container) {
    container.innerHTML = `
        <div class="d-flex align-items-center justify-content-center h-100">
            <div class="text-center">
                <i class="fas fa-check-circle text-success fa-3x mb-3"></i>
                <h5>No Chain Forks</h5>
                <p class="text-muted">No chain forks detected in this time range.</p>
            </div>
        </div>
    `;
}

function getParticipationColor(participation) {
    // Clamp participation to 0-1 range
    participation = Math.max(0, Math.min(1, participation));
    
    let r, g, b;
    
    if (participation >= 0.66) {
        // Green to dark green (66% to 100%)
        const t = (participation - 0.66) / 0.34; // 0 to 1
        // From bright green (0, 255, 0) to dark green (0, 128, 0)
        r = 0;
        g = Math.round(255 - 127 * t); // 255 to 128
        b = 0;
    } else if (participation >= 0.5) {
        // Yellow to green (50% to 66%)
        const t = (participation - 0.5) / 0.16; // 0 to 1
        r = Math.round(255 * (1 - t)); // 255 to 0
        g = 255; // Keep green at max
        b = 0;
    } else if (participation >= 0.3) {
        // Orange to yellow (30% to 50%)
        const t = (participation - 0.3) / 0.2; // 0 to 1
        r = 255;
        g = Math.round(165 + 90 * t); // 165 (orange) to 255 (yellow)
        b = 0;
    } else if (participation >= 0.1) {
        // Red to orange (10% to 30%)
        const t = (participation - 0.1) / 0.2; // 0 to 1
        r = 255;
        g = Math.round(165 * t); // 0 to 165 (orange)
        b = 0;
    } else if (participation > 0) {
        // Gray to red (0% to 10%)
        const t = participation / 0.1; // 0 to 1
        // From gray (128, 128, 128) to red (255, 0, 0)
        r = Math.round(128 + 127 * t); // 128 to 255
        g = Math.round(128 * (1 - t)); // 128 to 0
        b = Math.round(128 * (1 - t)); // 128 to 0
    } else {
        // Exactly 0% - gray
        r = 128;
        g = 128;
        b = 128;
    }
    
    return `rgb(${r}, ${g}, ${b})`;
}

function drawForkWithParticipationGradient(fork, forkX, startY, endY, svg, getSlotY) {
    const elements = [];
    const strokeWidth = '3';
    
    // Sort participation data by epoch
    const epochData = [...fork.participationByEpoch].sort((a, b) => a.epoch - b.epoch);
    
    if (epochData.length === 0) {
        return elements;
    }
    
    // Use dynamic slots per epoch
    const slotsPerEpoch = (window.chainDiagramData.specs && window.chainDiagramData.specs.slots_per_epoch) ? window.chainDiagramData.specs.slots_per_epoch : 32;
    
    // Create segments covering the full slot range
    const totalSlots = fork.headSlot - fork.baseSlot;
    if (totalSlots <= 0) {
        return elements;
    }
    
    // Find participation data for each epoch that the fork spans
    // This ensures we properly handle partial epochs at the beginning and end
    const segments = [];
    
    const baseEpoch = Math.floor(fork.baseSlot / slotsPerEpoch);
    const headEpoch = Math.floor(fork.headSlot / slotsPerEpoch);
    
    for (let epoch = baseEpoch; epoch <= headEpoch; epoch++) {
        // Calculate the slot range for this epoch that intersects with the fork
        const epochStartSlot = epoch * slotsPerEpoch;
        const epochEndSlot = (epoch + 1) * slotsPerEpoch - 1;
        
        // Find the actual intersection with the fork's slot range
        const segmentStartSlot = Math.max(fork.baseSlot, epochStartSlot);
        const segmentEndSlot = Math.min(fork.headSlot, epochEndSlot);
        
        // Skip if this epoch doesn't intersect with the fork
        if (segmentStartSlot > segmentEndSlot) continue;
        
        // Find participation for this epoch
        const epochParticipation = epochData.find(e => e.epoch === epoch);
        const participation = epochParticipation ? epochParticipation.participation : 0;
        
        // Use direct slot-to-Y conversion for exact epoch boundary positioning
        const segmentStartY = getSlotY(epochStartSlot);
        const segmentEndY = getSlotY(epochEndSlot + 1); // +1 because epochEndSlot is inclusive
        
        segments.push({
            epoch: epoch,
            startSlot: segmentStartSlot,
            endSlot: segmentEndSlot,
            startY: segmentStartY,
            endY: segmentEndY,
            participation: participation,
            hasData: !!epochParticipation
        });
    }
    
    // Draw segments
    for (let i = 0; i < segments.length; i++) {
        const segmentData = segments[i];
        const color = getParticipationColor(segmentData.participation);
        
        // Skip zero-length segments
        if (Math.abs(segmentData.endY - segmentData.startY) < 0.5) continue;
        
        // Draw segment line
        const segmentLine = document.createElementNS('http://www.w3.org/2000/svg', 'line');
        segmentLine.setAttribute('x1', forkX);
        segmentLine.setAttribute('y1', segmentData.startY);
        segmentLine.setAttribute('x2', forkX);
        segmentLine.setAttribute('y2', segmentData.endY);
        segmentLine.setAttribute('class', 'fork-line');
        segmentLine.setAttribute('stroke', color);
        segmentLine.setAttribute('data-fork-id', fork.forkId);
        segmentLine.setAttribute('data-epoch', segmentData.epoch);
        segmentLine.setAttribute('data-participation', (segmentData.participation * 100).toFixed(1));
        segmentLine.setAttribute('data-has-data', segmentData.hasData);
        segmentLine.setAttribute('stroke-width', strokeWidth);
        segmentLine.style.cursor = 'pointer';
        
        // Add wider invisible hit area for easier hovering
        const segmentHitArea = document.createElementNS('http://www.w3.org/2000/svg', 'line');
        segmentHitArea.setAttribute('x1', forkX);
        segmentHitArea.setAttribute('y1', segmentData.startY);
        segmentHitArea.setAttribute('x2', forkX);
        segmentHitArea.setAttribute('y2', segmentData.endY);
        segmentHitArea.setAttribute('stroke', 'transparent');
        segmentHitArea.setAttribute('stroke-width', '12'); // Much wider hit area
        segmentHitArea.setAttribute('data-fork-id', fork.forkId);
        segmentHitArea.setAttribute('data-hit-area', 'true'); // Mark as hit area to exclude from highlighting
        segmentHitArea.setAttribute('data-epoch', segmentData.epoch);
        segmentHitArea.setAttribute('data-participation', (segmentData.participation * 100).toFixed(1));
        segmentHitArea.setAttribute('data-has-data', segmentData.hasData);
        segmentHitArea.style.cursor = 'pointer';
        
        // Add tooltip on hover for segment details (both visible line and hit area)
        const addSegmentEvents = (element) => {
            element.addEventListener('mouseenter', (e) => {
                queueShowEpochTooltip(e, segmentData.epoch, segmentData.participation, segmentData.endSlot - segmentData.startSlot + 1, element);
            });
            element.addEventListener('mouseleave', () => {
                queueHideEpochTooltip(element);
            });
        };
        
        addSegmentEvents(segmentLine);
        addSegmentEvents(segmentHitArea);
        
        svg.appendChild(segmentLine);
        svg.appendChild(segmentHitArea);
        elements.push(segmentLine, segmentHitArea);
    }
    
    return elements;
}

function showEpochTooltip(event, epoch, participation, slotCount) {
    // Remove any existing tooltip
    hideEpochTooltip();
    
    const tooltip = document.createElement('div');
    tooltip.id = 'epoch-tooltip';
    tooltip.style.cssText = `
        position: absolute;
        background: rgba(0,0,0,0.9);
        color: white;
        padding: 8px 12px;
        border-radius: 4px;
        font-size: 12px;
        pointer-events: none;
        z-index: 1000;
        white-space: nowrap;
    `;
    
    const participationPercent = (participation * 100).toFixed(1);
    tooltip.innerHTML = `
        <strong>Epoch ${epoch}</strong><br>
        Participation: ${participationPercent}%<br>
        Slots: ${slotCount}
    `;
    
    document.body.appendChild(tooltip);
    
    // Position tooltip near cursor
    const updatePosition = (e) => {
        tooltip.style.left = (e.pageX + 10) + 'px';
        tooltip.style.top = (e.pageY - 40) + 'px';
    };
    
    updatePosition(event);
    
    // Store update function for mouse move
    tooltip._updatePosition = updatePosition;
    document.addEventListener('mousemove', updatePosition);
}

function hideEpochTooltip() {
    const tooltip = document.getElementById('epoch-tooltip');
    if (tooltip) {
        if (tooltip._updatePosition) {
            document.removeEventListener('mousemove', tooltip._updatePosition);
        }
        tooltip.remove();
    }
}

// Debounced tooltip functions to prevent stuttering
function queueShowEpochTooltip(event, epoch, participation, slotCount, element) {
    // Clear any pending hide timer
    if (tooltipQueue.hideTimer) {
        clearTimeout(tooltipQueue.hideTimer);
        tooltipQueue.hideTimer = null;
    }
    
    // If already showing the same element, do nothing
    if (tooltipQueue.isShowing && tooltipQueue.currentElement === element) {
        return;
    }
    
    // Clear any pending show timer
    if (tooltipQueue.showTimer) {
        clearTimeout(tooltipQueue.showTimer);
    }
    
    // Store pending show data
    tooltipQueue.pendingShow = { event, epoch, participation, slotCount, element };
    tooltipQueue.currentElement = element;
    
    // Set timer to show tooltip after 50ms
    tooltipQueue.showTimer = setTimeout(() => {
        // Only show if we're still hovering the same element
        if (tooltipQueue.currentElement === element && tooltipQueue.pendingShow) {
            const { event: e, epoch: ep, participation: part, slotCount: count } = tooltipQueue.pendingShow;
            showEpochTooltip(e, ep, part, count);
            tooltipQueue.isShowing = true;
            tooltipQueue.pendingShow = null;
        }
        tooltipQueue.showTimer = null;
    }, 50);
}

function queueHideEpochTooltip(element) {
    // Only process hide if we were showing or about to show this element
    if (tooltipQueue.currentElement !== element) {
        return;
    }
    
    // Clear any pending show timer
    if (tooltipQueue.showTimer) {
        clearTimeout(tooltipQueue.showTimer);
        tooltipQueue.showTimer = null;
        tooltipQueue.pendingShow = null;
    }
    
    // Only hide if we're actually showing a tooltip
    if (tooltipQueue.isShowing) {
        // Set timer to hide tooltip after a short delay to prevent flicker
        tooltipQueue.hideTimer = setTimeout(() => {
            hideEpochTooltip();
            tooltipQueue.isShowing = false;
            tooltipQueue.currentElement = null;
            tooltipQueue.hideTimer = null;
        }, 10);
    } else {
        // Not showing, just reset
        tooltipQueue.currentElement = null;
    }
}

function bytesToHex(bytes) {
    if (!bytes) return '';
    if (Array.isArray(bytes)) {
        return bytes.map(b => b.toString(16).padStart(2, '0')).join('');
    }
    try {
        const binary = atob(bytes);
        return Array.from(binary, c => c.charCodeAt(0).toString(16).padStart(2, '0')).join('');
    } catch (e) {
        return bytes.toString();
    }
}

function copyToClipboard(text) {
    navigator.clipboard.writeText(text).then(() => {
    }).catch(err => {
        console.error('Failed to copy:', err);
    });
}

// Calculate dynamic zoom step based on current zoom level
function getDynamicZoomStep(currentZoom) {
    // Logarithmic scaling: step size increases with zoom level
    // At 1x: step = 0.1
    // At 5x: step = 0.5  
    // At 10x: step = 1.0
    // At 20x: step = 2.0
    return ZOOM_STEP_BASE * Math.max(1, currentZoom);
}

// Zoom functionality with dynamic step size
function zoom(direction) {
    const dynamicStep = getDynamicZoomStep(zoomLevel);
    const actualDirection = direction > 0 ? dynamicStep : -dynamicStep;
    const newZoom = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, zoomLevel + actualDirection));
    if (newZoom !== zoomLevel) {
        previousZoomLevel = zoomLevel;
        zoomLevel = newZoom;
        applyZoom();
    }
}

function resetZoom() {
    previousZoomLevel = zoomLevel;
    zoomLevel = 1.0;
    applyZoom();
}

function handleWheelZoom(event) {
    // Only zoom when Ctrl key is held down
    if (!event.ctrlKey) {
        return; // Allow normal scrolling
    }
    
    event.preventDefault();
    const direction = event.deltaY > 0 ? -1 : 1;
    zoom(direction);
}

function applyZoom() {
    if (!scrollContainer) {
        renderChainForkTree();
        return;
    }

    // Store current state before re-render
    const oldScrollLeft = scrollContainer.scrollLeft;
    const oldScrollTop = scrollContainer.scrollTop;
    const oldZoom = previousZoomLevel;
    
    // Determine focus point in document coordinates
    let focusDocumentX, focusDocumentY, focusViewportX, focusViewportY;
    
    if (currentMousePos.documentX > 0 && currentMousePos.documentY > 0) {
        // Use mouse position as focus point
        focusDocumentX = currentMousePos.documentX;
        focusDocumentY = currentMousePos.documentY;
        focusViewportX = currentMousePos.viewportX;
        focusViewportY = currentMousePos.viewportY;
    } else {
        // Use viewport center as focus point
        focusViewportX = scrollContainer.clientWidth / 2;
        focusViewportY = scrollContainer.clientHeight / 2;
        focusDocumentX = oldScrollLeft + focusViewportX;
        focusDocumentY = oldScrollTop + focusViewportY;
    }

    // Re-render the diagram with the new zoom level
    renderChainForkTree();

    // Calculate new scroll position with higher precision
    if (scrollContainer && oldZoom > 0) {
        const zoomRatio = zoomLevel / oldZoom;
        
        // The point in the old document coordinate system becomes:
        // newDocumentCoord = oldDocumentCoord * zoomRatio
        // We want the focus point to remain at the same viewport position:
        // newScrollPos = newDocumentCoord - viewportOffset
        const newFocusDocumentX = focusDocumentX * zoomRatio;
        const newFocusDocumentY = focusDocumentY * zoomRatio;
        
        const newScrollLeft = Math.round(newFocusDocumentX - focusViewportX);
        const newScrollTop = Math.round(newFocusDocumentY - focusViewportY);
        
        // Apply the new scroll position
        scrollContainer.scrollLeft = Math.max(0, newScrollLeft);
        scrollContainer.scrollTop = Math.max(0, newScrollTop);
        
        // Update mouse position for next zoom operation
        if (currentMousePos.documentX > 0) {
            currentMousePos.documentX = newFocusDocumentX;
            currentMousePos.documentY = newFocusDocumentY;
        }
    }
}

// Hide single-block forks and merge chains into single lines
function hideAndMergeSingleBlockForks(forks) {
    
    // Step 1: Build parent-child relationships first to identify which single-block forks have children
    const tempChildrenMap = new Map();
    for (const fork of forks) {
        if (!tempChildrenMap.has(fork.parentFork)) {
            tempChildrenMap.set(fork.parentFork, []);
        }
        tempChildrenMap.get(fork.parentFork).push(fork.forkId);
    }
    
    // Step 2: Filter out single-block forks ONLY if they don't have children
    const filteredForks = forks.filter(fork => {
        // Always keep canonical chain (fork ID 0) and explicitly canonical forks
        const isCanonical = fork.forkId === 0 || (fork.isCanonical !== undefined ? fork.isCanonical : false);
        if (isCanonical) {
            return true;
        }
        
        const blockCount = fork.blockCount || 0;
        if (blockCount === 1) {
            const children = tempChildrenMap.get(fork.forkId) || [];
            if (children.length > 0) {
                return true; // Keep it - it's a branching point
            } else {
                return false; // Remove it - it's just clutter
            }
        }
        return true; // Keep all non-single-block forks
    });
    
    // Step 3: Build parent-child relationships for remaining forks
    const forkMap = new Map();
    const childrenMap = new Map();
    
    for (const fork of filteredForks) {
        forkMap.set(fork.forkId, fork);
        if (!childrenMap.has(fork.parentFork)) {
            childrenMap.set(fork.parentFork, []);
        }
        childrenMap.get(fork.parentFork).push(fork.forkId);
    }
    
    // Step 4: Find chains to merge (follow single-child chains with same canonical status)
    const processed = new Set();
    const result = [];
    
    for (const fork of filteredForks) {
        if (processed.has(fork.forkId)) continue;
        
        // Find the complete chain starting from this fork (only merge same canonical status)
        const chain = buildMergeChain(fork.forkId, forkMap, childrenMap);
        
        if (chain.length === 1) {
            // No chain, keep as-is
            result.push({ ...fork });
            processed.add(fork.forkId);
        } else {
            // Merge the entire chain
            const firstFork = forkMap.get(chain[0]);
            const lastFork = forkMap.get(chain[chain.length - 1]);
            
            // Preserve the canonical flag from the first fork in the chain
            const isCanonical = firstFork.isCanonical !== undefined ? firstFork.isCanonical : false;
            
            const mergedFork = {
                forkId: lastFork.forkId,  // Use HEAD fork ID, not first
                baseSlot: firstFork.baseSlot,
                baseRoot: firstFork.baseRoot,
                leafSlot: firstFork.leafSlot,
                leafRoot: firstFork.leafRoot,
                headSlot: lastFork.headSlot,
                headRoot: lastFork.headRoot,
                length: lastFork.headSlot - firstFork.baseSlot + 1,
                blockCount: chain.reduce((sum, forkId) => sum + (forkMap.get(forkId).blockCount || 0), 0),
                participation: calculateAverageParticipation(chain.map(id => forkMap.get(id))),
                participationByEpoch: mergeParticipationByEpoch(chain.map(id => forkMap.get(id))),
                position: lastFork.position,  // Use HEAD fork position
                parentFork: firstFork.parentFork,
                isCanonical: isCanonical,  // PRESERVE CANONICAL FLAG
                mergedForkIds: chain
            };
            
            result.push(mergedFork);
            
            // Mark all in chain as processed
            for (const forkId of chain) {
                processed.add(forkId);
            }
        }
    }
    
    
    // Step 5: Reassign positions to use available space optimally
    const reorganizedForks = reassignForkPositions(result);
    
    return reorganizedForks;
}

// Build a chain of forks that should be merged (following single-child paths with same canonical status)
function buildMergeChain(startForkId, forkMap, childrenMap) {
    const chain = [startForkId];
    let currentForkId = startForkId;
    const startFork = forkMap.get(startForkId);
    const startCanonical = startFork.isCanonical !== undefined ? startFork.isCanonical : false;
    
    // Follow the chain forward (child direction) - only merge forks with same canonical status
    while (true) {
        const children = childrenMap.get(currentForkId) || [];
        if (children.length === 1) {
            const childFork = forkMap.get(children[0]);
            const childCanonical = childFork.isCanonical !== undefined ? childFork.isCanonical : false;
            
            // Only extend chain if canonical status matches
            if (childCanonical === startCanonical) {
                currentForkId = children[0];
                chain.push(currentForkId);
            } else {
                // Canonical status differs - end the chain
                break;
            }
        } else {
            // Multiple or no children - end the chain
            break;
        }
    }
    
    return chain;
}


function calculateAverageParticipation(forks) {
    let totalParticipation = 0;
    let count = 0;
    
    for (const fork of forks) {
        if (fork.participation > 0) {
            totalParticipation += fork.participation;
            count++;
        }
    }
    
    return count > 0 ? totalParticipation / count : 0;
}

function mergeParticipationByEpoch(forks) {
    const epochMap = new Map();
    
    for (const fork of forks) {
        if (fork.participationByEpoch) {
            for (const epochData of fork.participationByEpoch) {
                const key = epochData.epoch;
                if (!epochMap.has(key)) {
                    epochMap.set(key, { ...epochData });
                } else {
                    // Average the participation values
                    const existing = epochMap.get(key);
                    existing.participation = (existing.participation + epochData.participation) / 2;
                    existing.slotCount += epochData.slotCount;
                }
            }
        }
    }
    
    return Array.from(epochMap.values()).sort((a, b) => a.epoch - b.epoch);
}

// Calculate 5-epoch rolling average participation for a fork at a specific time
function calculate5EpochParticipation(fork, targetEpoch) {
    if (!fork.participationByEpoch || fork.participationByEpoch.length === 0) {
        return fork.participation || 0; // Fallback to overall participation
    }
    
    // Find participation data around the target epoch (±2 epochs for 5-epoch window)
    const relevantEpochs = fork.participationByEpoch.filter(epochData => 
        epochData.epoch >= targetEpoch - 2 && epochData.epoch <= targetEpoch + 2
    );
    
    if (relevantEpochs.length === 0) {
        return fork.participation || 0; // Fallback to overall participation
    }
    
    // Calculate weighted average based on slot count
    let totalParticipation = 0;
    let totalSlots = 0;
    
    for (const epochData of relevantEpochs) {
        totalParticipation += epochData.participation * (epochData.slotCount || 1);
        totalSlots += (epochData.slotCount || 1);
    }
    
    return totalSlots > 0 ? totalParticipation / totalSlots : (fork.participation || 0);
}

// Reassign fork positions with participation-based ordering for high-participation forks
function reassignForkPositions(forks) {
    
    // Build parent-child relationships
    const childrenMap = new Map();
    for (const fork of forks) {
        const parentFork = fork.parentFork || fork.parent_fork || 0;
        if (parentFork !== 0) {
            if (!childrenMap.has(parentFork)) {
                childrenMap.set(parentFork, []);
            }
            childrenMap.get(parentFork).push(fork);
        }
    }
    
    // Sort children by fork ID for deterministic ordering
    for (const children of childrenMap.values()) {
        children.sort((a, b) => (a.forkId || a.fork_id) - (b.forkId || b.fork_id));
    }
    
    // Sort forks by base slot for chronological processing
    const sortedForks = [...forks].sort((a, b) => {
        const aBaseSlot = a.baseSlot || a.base_slot;
        const bBaseSlot = b.baseSlot || b.base_slot;
        if (aBaseSlot === bBaseSlot) {
            return (a.forkId || a.fork_id || 0) - (b.forkId || b.fork_id || 0);
        }
        return aBaseSlot - bBaseSlot;
    });
    
    // Track positions and lane occupancy
    const positions = new Map();
    const lanes = new Map(); // lane -> { endSlot, forkId, participation }
    const slotsPerEpoch = (window.chainDiagramData.specs && window.chainDiagramData.specs.slots_per_epoch) ? window.chainDiagramData.specs.slots_per_epoch : 32;
    
    // Separate forks into canonical, high-participation (≥5%), and low-participation (<5%)
    const canonicalForks = [];
    const highParticipationForks = [];
    const lowParticipationForks = [];
    
    for (const fork of sortedForks) {
        const forkId = fork.forkId || fork.fork_id;
        const isCanonical = fork.isCanonical || fork.is_canonical || false;
        if (isCanonical) {
            canonicalForks.push(fork);
        } else {
            const baseSlot = fork.baseSlot || fork.base_slot;
            const baseEpoch = Math.floor(baseSlot / slotsPerEpoch);
            const currentParticipation = calculate5EpochParticipation(fork, baseEpoch);
            
            
            if (currentParticipation >= 0.05) { // 5% threshold
                highParticipationForks.push({ 
                    ...fork, 
                    currentParticipation: currentParticipation 
                });
            } else {
                lowParticipationForks.push({ 
                    ...fork, 
                    currentParticipation: currentParticipation 
                });
            }
        }
    }
    
    // Sort high-participation forks by participation (descending)
    highParticipationForks.sort((a, b) => b.currentParticipation - a.currentParticipation);
    
    
    const result = [];
    
    // Step 1: Assign canonical forks to position 0
    for (const fork of canonicalForks) {
        const forkId = fork.forkId || fork.fork_id;
        const baseSlot = fork.baseSlot || fork.base_slot;
        const headSlot = fork.headSlot || fork.head_slot;
        
        positions.set(forkId, 0);
        lanes.set(0, { endSlot: headSlot, forkId: forkId, participation: 1.0 });
        
        result.push({
            ...fork,
            position: 0
        });
    }
    
    // Step 2: Process ALL forks chronologically, using participation to decide who gets parent lanes at splits
    // Combine all forks (high and low participation) and process in chronological order
    const allNonCanonicalForks = [...highParticipationForks, ...lowParticipationForks].sort((a, b) => {
        const aBaseSlot = a.baseSlot || a.base_slot;
        const bBaseSlot = b.baseSlot || b.base_slot;
        if (aBaseSlot === bBaseSlot) {
            return (a.forkId || a.fork_id || 0) - (b.forkId || b.fork_id || 0);
        }
        return aBaseSlot - bBaseSlot;
    });
    
    for (const fork of allNonCanonicalForks) {
        const forkId = fork.forkId || fork.fork_id;
        const baseSlot = fork.baseSlot || fork.base_slot;
        const headSlot = fork.headSlot || fork.head_slot;
        const parentFork = fork.parentFork || fork.parent_fork || 0;
        const participation = fork.currentParticipation || 0;
        
        // Check if parent has a position and get all children for this parent
        const parentPosition = positions.get(parentFork);
        const children = childrenMap.get(parentFork) || [];
        
        let position;
        
        if (parentPosition !== undefined && parentPosition !== 0 && children.length > 0) {
            // Multiple children - decide who continues in parent lane based on PARTICIPATION
            // Calculate currentParticipation for all children first
            const childrenWithParticipation = children.map(child => {
                const childBaseSlot = child.baseSlot || child.base_slot;
                const childBaseEpoch = Math.floor(childBaseSlot / slotsPerEpoch);
                const childCurrentParticipation = calculate5EpochParticipation(child, childBaseEpoch);
                return {
                    ...child,
                    currentParticipation: childCurrentParticipation
                };
            });
            
            // Sort siblings by participation (descending)
            const sortedChildren = [...childrenWithParticipation].sort((a, b) => {
                const aParticipation = a.currentParticipation || a.participation || 0;
                const bParticipation = b.currentParticipation || b.participation || 0;
                return bParticipation - aParticipation;
            });
            
            const highestParticipationChild = sortedChildren[0];
            const highestParticipationChildId = highestParticipationChild.forkId || highestParticipationChild.fork_id;
            
            if (forkId === highestParticipationChildId) {
                // This fork has highest participation among siblings - continue in parent lane
                const epochCooldown = (window.chainDiagramData.specs && window.chainDiagramData.specs.slots_per_epoch) ? window.chainDiagramData.specs.slots_per_epoch : 32;
                const laneInfo = lanes.get(parentPosition);
                const laneAvailable = !laneInfo || laneInfo.endSlot <= baseSlot || laneInfo.endSlot + epochCooldown < baseSlot;
                
                if (laneAvailable) {
                    position = parentPosition;
                } else {
                    // Parent lane not available - get any available position
                    position = findNextAvailableLaneJS(lanes, baseSlot, window.chainDiagramData.specs);
                }
            } else {
                // Not the highest participation child - get a new lane (skip parent lane)
                position = findNextAvailableLaneJS(lanes, baseSlot, window.chainDiagramData.specs, parentPosition);
            }
        } else {
            // No parent position or single child - get any available position
            position = findNextAvailableLaneJS(lanes, baseSlot, window.chainDiagramData.specs);
        }
        
        positions.set(forkId, position);
        lanes.set(position, { endSlot: headSlot, forkId: forkId, participation: participation });
        
        result.push({
            ...fork,
            position: position
        });
    }
    
    // Sort result back to original chronological order
    result.sort((a, b) => {
        const aBaseSlot = a.baseSlot || a.base_slot;
        const bBaseSlot = b.baseSlot || b.base_slot;
        if (aBaseSlot === bBaseSlot) {
            return (a.forkId || a.fork_id || 0) - (b.forkId || b.fork_id || 0);
        }
        return aBaseSlot - bBaseSlot;
    });
    
    return result;
}

// Helper function to find next available lane
function findNextAvailableLaneJS(lanes, baseSlot, specs, skipPosition = null) {
    let position = 1; // Start from lane 1 (lane 0 is canonical)
    
    // Add 1-epoch cooldown period after a fork ends
    const epochCooldown = (specs && specs.slots_per_epoch) ? specs.slots_per_epoch : 32;
    
    while (true) {
        // Skip the specified position (used to skip parent lane for non-winner children)
        if (position === skipPosition) {
            position++;
            continue;
        }
        
        const laneInfo = lanes.get(position);
        if (!laneInfo || laneInfo.endSlot <= baseSlot || laneInfo.endSlot + epochCooldown < baseSlot) {
            // Lane is free, allows immediate continuation, or fork starts after cooldown period
            break;
        }
        position++;
    }
    
    return position;
}

// Find next available lane for high-participation forks (reserved positions 1-N based on participation)
function findHighParticipationLane(lanes, baseSlot, specs, targetPosition) {
    const epochCooldown = (specs && specs.slots_per_epoch) ? specs.slots_per_epoch : 32;
    
    // Try the target position first
    const laneInfo = lanes.get(targetPosition);
    if (!laneInfo || laneInfo.endSlot <= baseSlot || laneInfo.endSlot + epochCooldown < baseSlot) {
        return targetPosition;
    }
    
    // If target position is occupied, find next available position
    return findNextAvailableLaneJS(lanes, baseSlot, specs);
}