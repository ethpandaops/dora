/**
 * Chain Fork Tree Visualization
 * Shows canonical chain and forks as a proper tree structure
 * Each fork is a vertical line from base to head
 * Horizontal axis only for positioning without overlap
 */

let zoomLevel = 1.0;
const ZOOM_MIN = 0.5;
const ZOOM_MAX = 3.0;
const ZOOM_STEP = 0.1;

document.addEventListener('DOMContentLoaded', function() {
    if (typeof window.chainDiagramData === 'undefined') {
        console.warn('Chain diagram data not found');
        return;
    }
    
    renderChainForkTree();
    
    // Add zoom event listeners
    const zoomInBtn = document.getElementById('zoomIn');
    const zoomOutBtn = document.getElementById('zoomOut');
    const zoomResetBtn = document.getElementById('zoomReset');
    
    if (zoomInBtn) zoomInBtn.addEventListener('click', () => zoom(ZOOM_STEP));
    if (zoomOutBtn) zoomOutBtn.addEventListener('click', () => zoom(-ZOOM_STEP));
    if (zoomResetBtn) zoomResetBtn.addEventListener('click', resetZoom);
    
    // Add mouse wheel zoom - will be attached after diagram creation
    // This is handled in renderChainForkTree after creating the scroll container
    
    // Add hide single-block forks checkbox listener
    const hideCheckbox = document.getElementById('hideSingleBlockForks');
    if (hideCheckbox) {
        hideCheckbox.addEventListener('change', renderChainForkTree);
    }
});

function renderChainForkTree() {
    const container = document.getElementById('chainDiagram');
    if (!container) {
        console.error('Chain diagram container not found');
        return;
    }
    
    const data = window.chainDiagramData;
    let diagram = data.diagram;
    
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
        parentFork: fork.parent_fork !== undefined ? fork.parent_fork : (fork.parentFork || 0)
    }));
    
    // Check if we should hide single-block forks
    const hideCheckbox = document.getElementById('hideSingleBlockForks');
    const hideSingleBlockForks = hideCheckbox && hideCheckbox.checked;
    
    if (hideSingleBlockForks) {
        diagram.forks = hideAndMergeSingleBlockForks(diagram.forks);
    }
    
    
    if (diagram.forks.length === 0) {
        renderEmptyDiagram(container);
        return;
    }
    
    // Calculate dimensions
    const containerAvailableWidth = container.offsetWidth - 40;
    
    // Calculate epoch range first to determine required height
    const startEpoch = data.startEpoch;
    const endEpoch = data.endEpoch;
    const epochRange = endEpoch - startEpoch;
    
    // Ensure each 5-epoch block is at least 50px
    const minHeightPer5Epochs = 50;
    const epochBlocks = Math.ceil(epochRange / 5);
    const minRequiredHeight = epochBlocks * minHeightPer5Epochs;
    
    // Use the larger of our minimum requirement or the default height
    const containerHeight = Math.max(600, 800, minRequiredHeight + 80); // +80 for margins
    
    // Calculate required width based on number of forks
    const forkSpacing = 80; // Same as in getTreeX function
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
    
    // Calculate time scale with minimum height constraint
    const timeScale = epochRange > 0 ? drawingHeight / epochRange : 1;
    
    
    
    // Helper functions
    function getEpochY(epoch) {
        // Head (newest) at top, older epochs below  
        const y = margin.top + ((endEpoch - epoch) * timeScale);
        return y;
    }
    
    // Convert slot to epoch for Y positioning
    function getSlotY(slot) {
        const slotsPerEpoch = (data.specs && data.specs.slots_per_epoch) ? data.specs.slots_per_epoch : 32; // Use dynamic value with fallback
        const epoch = Math.floor(slot / slotsPerEpoch);
        const y = getEpochY(epoch);
        
        
        return y;
    }
    
    function getTreeX(position) {
        // Git-style positioning: canonical on left, forks to the right
        const forkSpacing = 80; // Increased spacing between fork lines for better readability
        return margin.left + 40 + (position * forkSpacing); // Start from left margin + padding
    }
    
    function getParticipationColor(participation) {
        if (participation <= 0) return '#6c757d'; // Unknown/missing participation - gray
        
        // Clamp participation to 0-1 range
        participation = Math.max(0, Math.min(1, participation));
        
        let r, g, b;
        
        if (participation <= 0.2) {
            // Red zone (0% to 20%): Pure red to red-orange
            const t = participation / 0.2; // 0 to 1
            r = 255;
            g = Math.round(50 * t); // Add slight orange tint at 20%
            b = 0;
        } else if (participation <= 0.5) {
            // Red-orange to yellow-orange (20% to 50%)
            const t = (participation - 0.2) / 0.3; // 0 to 1
            r = 255;
            g = Math.round(50 + 155 * t); // 50 to 205 (light orange at 50%)
            b = 0;
        } else if (participation <= 0.8) {
            // Yellow-orange to yellow (50% to 80%)
            const t = (participation - 0.5) / 0.3; // 0 to 1
            r = Math.round(255 - 55 * t); // 255 to 200 (remove some red for yellow)
            g = Math.round(205 + 50 * t); // 205 to 255 (increase green to full yellow)
            b = 0;
        } else {
            // Yellow to green (80% to 100%)
            const t = (participation - 0.8) / 0.2; // 0 to 1
            r = Math.round(200 * (1 - t)); // 200 to 0 (remove red completely)
            g = 255; // Keep green at max
            b = 0;
        }
        
        return `rgb(${r}, ${g}, ${b})`;
    }
    
    
    // Draw time grid (horizontal lines) - showing every 5 epochs
    const epochStep = 5;
    for (let epoch = startEpoch; epoch <= endEpoch; epoch += epochStep) {
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
    const finalitySlot = data.finalitySlot;
    if (finalitySlot) {
        const slotsPerEpoch = (data.specs && data.specs.slots_per_epoch) ? data.specs.slots_per_epoch : 32;
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
        
        
        // Make sure fork lines have a minimum visible height
        const minForkHeight = 20; // Minimum height for visibility
        const actualHeight = Math.abs(baseY - headY);
        let adjustedHeadY = headY;
        
        if (actualHeight < minForkHeight && fork.baseSlot !== fork.headSlot) {
            // Only extend if fork actually spans multiple slots
            // Extend upward (toward newer slots) to avoid going below slot 0
            adjustedHeadY = baseY - minForkHeight;
        } else if (fork.baseSlot === fork.headSlot) {
            // For single-slot forks, just show a small dot, not a line
            adjustedHeadY = headY;
        }
        
        // Validate calculated positions
        if (isNaN(baseY) || isNaN(adjustedHeadY) || isNaN(forkX)) {
            console.warn('Invalid position calculated for fork:', fork, { baseY, headY: adjustedHeadY, forkX });
            return;
        }
        
        // Store the adjusted head Y position for this fork
        forkHeadY[fork.forkId] = adjustedHeadY;
        
        // Calculate participation color for elements outside the main fork line
        const participationColor = getParticipationColor(fork.participation);
        
        // Draw fork timeline with participation gradients
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
                forkElements.push(forkLine);
            }
        }
        
        // Draw branching connection (horizontal line from parent to fork start)
        // Only draw branching lines when the fork is in a different position than its parent
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
                // Draw horizontal branching line
                const branchLine = document.createElementNS('http://www.w3.org/2000/svg', 'line');
                branchLine.setAttribute('x1', parentX + 6);
                branchLine.setAttribute('y1', branchY);
                branchLine.setAttribute('x2', forkX);
                branchLine.setAttribute('y2', branchY);
                branchLine.setAttribute('class', 'fork-line');
                branchLine.setAttribute('stroke', participationColor);
                branchLine.setAttribute('stroke-width', '3');
                branchLine.setAttribute('data-fork-id', fork.forkId);
                branchLine.setAttribute('stroke-dasharray', '3,3');
                branchLine.style.cursor = 'pointer';
                branchLine.style.opacity = '0.7';
                svg.appendChild(branchLine);
                
                // Removed branch point circle - only showing head bubbles
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
        
        // Recalculate adjusted head Y
        const minForkHeight = 20;
        const actualHeight = Math.abs(baseY - headY);
        let adjustedHeadY = headY;
        if (actualHeight < minForkHeight && fork.baseSlot !== fork.headSlot) {
            adjustedHeadY = baseY - minForkHeight;
        }
        
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
        
        // Block count label (actual number of blocks in this fork)
        const blockCountLabel = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        blockCountLabel.setAttribute('x', forkX + 12);
        blockCountLabel.setAttribute('y', (baseY + adjustedHeadY) / 2);
        blockCountLabel.setAttribute('class', 'block-count-label');
        blockCountLabel.textContent = `${fork.blockCount || fork.block_count || 0} blocks`;
        svg.appendChild(blockCountLabel);
        
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
                el.addEventListener('mouseenter', () => highlightFork(fork.forkId));
                el.addEventListener('mouseleave', () => unhighlightFork(fork.forkId));
            }
        });
    });
    
    // Clear container and create scroll wrapper
    container.innerHTML = '';
    
    // Create scroll container
    const scrollContainer = document.createElement('div');
    scrollContainer.className = 'diagram-scroll-container';
    scrollContainer.appendChild(svg);
    
    // Add mouse wheel zoom to scroll container
    scrollContainer.addEventListener('wheel', handleWheelZoom, { passive: false });
    
    container.appendChild(scrollContainer);
    
    // Apply current zoom level
    applyZoom();
    
    // Show initial overview in details panel
    showOverview();
}

function highlightFork(forkId) {
    const elements = document.querySelectorAll(`[data-fork-id="${forkId}"]`);
    
    elements.forEach(el => {
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

function showForkDetails(fork) {
    const panel = document.getElementById('forkDetailsPanel');
    
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
                    ${isCanonical || !fork.headRoot ? 
                        `<a href="/slot/${headSlot}">${headSlot.toLocaleString()}</a>` :
                        `<a href="/slot/0x${bytesToHex(fork.headRoot)}" title="0x${bytesToHex(fork.headRoot)}">
                            ${headSlot.toLocaleString()} (0x${bytesToHex(fork.headRoot).substring(0, 8)}...)
                        </a>`
                    }
                </span>
                
                <span class="detail-label">Slot Range:</span>
                <span class="detail-value">${length.toLocaleString()} slots</span>
                
                <span class="detail-label">Block Count:</span>
                <span class="detail-value">${(fork.blockCount || 0).toLocaleString()} blocks</span>
                
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
    const data = window.chainDiagramData;
    let diagram = data.diagram;
    
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
                <span class="detail-value">Epochs ${window.chainDiagramData.startEpoch.toLocaleString()} - ${window.chainDiagramData.endEpoch.toLocaleString()}</span>
                
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
    if (participation <= 0) return '#dc3545'; // Unknown/missing participation - red
    
    // Clamp participation to 0-1 range
    participation = Math.max(0, Math.min(1, participation));
    
    let r, g, b;
    
    if (participation <= 0.2) {
        // Red zone (0% to 20%): Pure red to red-orange
        const t = participation / 0.2; // 0 to 1
        r = 255;
        g = Math.round(50 * t); // Add slight orange tint at 20%
        b = 0;
    } else if (participation <= 0.5) {
        // Red-orange to yellow-orange (20% to 50%)
        const t = (participation - 0.2) / 0.3; // 0 to 1
        r = 255;
        g = Math.round(50 + 155 * t); // 50 to 205 (light orange at 50%)
        b = 0;
    } else if (participation <= 0.8) {
        // Yellow-orange to yellow (50% to 80%)
        const t = (participation - 0.5) / 0.3; // 0 to 1
        r = Math.round(255 - 55 * t); // 255 to 200 (remove some red for yellow)
        g = Math.round(205 + 50 * t); // 205 to 255 (increase green to full yellow)
        b = 0;
    } else {
        // Yellow to green (80% to 100%)
        const t = (participation - 0.8) / 0.2; // 0 to 1
        r = Math.round(200 * (1 - t)); // 200 to 0 (remove red completely)
        g = 255; // Keep green at max
        b = 0;
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
        
        // Calculate Y positions based on slot progression
        const startSlotProgress = (segmentStartSlot - fork.baseSlot) / totalSlots;
        const endSlotProgress = (segmentEndSlot - fork.baseSlot) / totalSlots;
        
        const segmentStartY = startY + (startSlotProgress * (endY - startY)) + 0.5;
        const segmentEndY = startY + (endSlotProgress * (endY - startY)) - 0.5;
        
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
        
        // Add tooltip on hover for segment details
        segmentLine.addEventListener('mouseenter', (e) => {
            showEpochTooltip(e, segmentData.epoch, segmentData.participation, segmentData.endSlot - segmentData.startSlot + 1);
        });
        segmentLine.addEventListener('mouseleave', () => {
            hideEpochTooltip();
        });
        
        svg.appendChild(segmentLine);
        elements.push(segmentLine);
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
        console.log('Copied to clipboard:', text);
    }).catch(err => {
        console.error('Failed to copy:', err);
    });
}

// Zoom functionality
function zoom(direction) {
    const newZoom = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, zoomLevel + direction));
    if (newZoom !== zoomLevel) {
        zoomLevel = newZoom;
        applyZoom();
    }
}

function resetZoom() {
    zoomLevel = 1.0;
    applyZoom();
}

function handleWheelZoom(event) {
    event.preventDefault();
    const delta = event.deltaY > 0 ? -ZOOM_STEP : ZOOM_STEP;
    zoom(delta);
}

function applyZoom() {
    const svg = document.querySelector('#chainDiagram svg');
    if (svg) {
        // Apply zoom transform to the SVG
        svg.style.transform = `scale(${zoomLevel})`;
        svg.style.transformOrigin = 'top left';
        
        // The scroll container will handle overflow automatically
        // We don't need to change container sizes since overflow is handled by CSS
    }
    
    // Update button states
    const zoomInBtn = document.getElementById('zoomIn');
    const zoomOutBtn = document.getElementById('zoomOut');
    
    if (zoomInBtn) zoomInBtn.disabled = zoomLevel >= ZOOM_MAX;
    if (zoomOutBtn) zoomOutBtn.disabled = zoomLevel <= ZOOM_MIN;
}

// Hide single-block forks and merge chains into single lines
function hideAndMergeSingleBlockForks(forks) {
    console.log('hideAndMergeSingleBlockForks called with', forks.length, 'forks');
    
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
        const blockCount = fork.blockCount || 0;
        if (blockCount === 1) {
            const children = tempChildrenMap.get(fork.forkId) || [];
            if (children.length > 0) {
                console.log(`Keeping single-block fork ${fork.forkId} because it has ${children.length} children: [${children.join(', ')}]`);
                return true; // Keep it - it's a branching point
            } else {
                console.log(`Removing single-block fork ${fork.forkId} - no children`);
                return false; // Remove it - it's just clutter
            }
        }
        return true; // Keep all non-single-block forks
    });
    console.log(`Removed ${forks.length - filteredForks.length} single-block forks (kept those with children)`);
    
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
    
    // Step 4: Find chains to merge (follow single-child chains)
    const processed = new Set();
    const result = [];
    
    for (const fork of filteredForks) {
        if (processed.has(fork.forkId)) continue;
        
        // Find the complete chain starting from this fork
        const chain = buildMergeChain(fork.forkId, forkMap, childrenMap);
        
        if (chain.length === 1) {
            // No chain, keep as-is
            result.push({ ...fork });
            processed.add(fork.forkId);
        } else {
            // Merge the entire chain
            const firstFork = forkMap.get(chain[0]);
            const lastFork = forkMap.get(chain[chain.length - 1]);
            
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
                mergedForkIds: chain
            };
            
            console.log(`Merged chain [${chain.join(' -> ')}] into single fork ${mergedFork.forkId}`);
            result.push(mergedFork);
            
            // Mark all in chain as processed
            for (const forkId of chain) {
                processed.add(forkId);
            }
        }
    }
    
    console.log(`Final result: input ${forks.length} forks, output ${result.length} forks`);
    return result;
}

// Build a chain of forks that should be merged (following single-child paths)
function buildMergeChain(startForkId, forkMap, childrenMap) {
    const chain = [startForkId];
    let currentForkId = startForkId;
    
    // Follow the chain forward (child direction)
    while (true) {
        const children = childrenMap.get(currentForkId) || [];
        if (children.length === 1) {
            // Single child - extend the chain
            currentForkId = children[0];
            chain.push(currentForkId);
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