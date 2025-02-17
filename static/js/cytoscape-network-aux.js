$_network = {};

// Select a node and its neighborhood edges
$_network.selectNode = function (cy, nodeId) {
  elem = cy.getElementById(nodeId)
  cy.elements().unselect();
  elem.select();
  console.log(elem.neighborhood());
  elem.neighborhood().edges().select();
}

// Isolate a node and only show it and its neighborhood
$_network.isolateNode = function (cy, nodeId) {
  elem = cy.getElementById(nodeId)
  cy.elements().hide().unselect();
  elem.show().select();
  edges = elem.neighborhood().edges();
  edges.show();
  edges.select();
  elem.neighborhood().nodes().show();
}

// Show all nodes and edges
$_network.showAll = function (cy) {
  cy.elements().show().unselect();
}

// Available Layouts
$_network.layouts = {
  fcose : function(nodeCount){
    return {
      name: 'fcose',
      idealEdgeLength: nodeCount < 50 ? nodeCount * 5 : nodeCount * 3,
      nestingFactor: 1.4,
      nodeRepulsion: nodeCount * 1000,
      edgeElasticity: 0.45,
      gravity: 0.25,
      numIter: 2500,
      tile: true,
      tilingPaddingVertical: 10,
      tilingPaddingHorizontal: 10,
      animate: false,
      fit: true,
      padding: nodeCount < 20 ? 100 : 30,
      randomize: true,
      stop: function() {
        $("#nodemap-loading").hide();
      },
    }
  },
  circle : function() {
    return {
      name: 'circle',
      animate: false,
    }
  },
  grid : function() {
    return {
      name: 'grid',
      animate: false,
    }
  },
  concentric : function(nodeCount) {
    return {
      name: 'concentric',
      animate: false,
      padding: nodeCount < 20 ? 200 : 0,
      concentric: function( node ){
        return node.data('group') == 'internal' ? 2 : 1;
      },
      levelWidth: function( nodes ){
        return 1;
      }
    }
  },
}

// Default layout
$_network.defaultLayout = $_network.layouts.fcose;


// Default stylesheet
$_network.defaultStylesheet = cytoscape.stylesheet()
  .selector('node')
    .css({
      'height': 20,
      'width': 20,
      'background-fit': 'cover',
      'border-color': '#0077B6',
      'border-width': 1,
      'border-opacity': 1,
      'shape': 'ellipse',
    })
  .selector('edge')
    .css({
      'curve-style': 'bezier',
      'width': 0.5,
      'target-arrow-shape': 'vee',
      'line-color': '#0077B6',
      'target-arrow-color': '#0077B6',
      'arrow-scale': 0.5,
    })
  .selector('node[label]')
    .css({
      'label': 'data(label)',
    })
  .selector('.bottom-center')
    .css({
      "text-valign": "bottom",
      "text-halign": "center",
      "color": "#ffffff",
      "font-size": 4,
    })
  .selector('edge[interaction = "external"]')
    .css({
      'line-style': 'dashed',
      'line-color': '#075e4d',
      'target-arrow-color': '#075e4d',
      'source-arrow-color': '#075e4d',
    })
  .selector('node[group = "internal"]')
    .css({
      'border-color': '#FFA500',
      'background-color': '#FFA500',
      'opacity': 1
    })
  .selector('node[group = "external"]')
    .css({
      'border-color': '#075e4d',
      'background-color': '#075e4d',
      'opacity': 0.5,
      'height': 15,
      'width': 15,
      'border-style:': 'dashed',
      'shape': 'round-octagon',
    })
  .selector('node:selected, edge:selected')
    .css({
      'border-color': '#FFA500',
      'background-color': '#FFA500',
      'line-color': '#FFA500',
      'target-arrow-color': '#FFA500',
      'source-arrow-color': '#FFA500',
      'opacity': 1
});


$_network.fitAnimated = function (cy, layout) {
  cy.animate({
    fit: { padding: 20 },
    duration: 500,
    complete: function () {
      setTimeout(function () {
        layout.animate = true;
        layout.animationDuration = 2000;
        layout.fit = true;
        layout.directed = true;
        cy.layout(layout).run();
      }, 500);
    },
  });
}


// Create a cytoscape network
$_network.create = function (container, data){
  var stylesheet = $_network.defaultStylesheet;
  var cytoElements = [];
  for (var i = 0; i < data.nodes.length; i++) {
    // Create nodes
    data.nodes[i].title = data.nodes[i].id;
    if (data.nodes[i].id != "") {
      cytoElements.push(
        {
          data: data.nodes[i],
          classes: "bottom-center",
        }
      );
      svgIdenticon = jdenticon.toSvg(data.nodes[i].id, 80);
      // Add style to nodes
      stylesheet.selector('#' + data.nodes[i].id).css({
          'background-image': 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svgIdenticon),
          'background-fit': 'cover',
          'background-opacity': 1,
          'background-color': '#111111',
      });
    }
  }
  for (var i = 0; i < data.edges.length; i++) {
    // Create edges
    cytoElements.push({
      data: {
        id: data.edges[i].from + "-" + data.edges[i].to,
        source: data.edges[i].from,
        target: data.edges[i].to,
        interaction: data.edges[i].interaction,
      }
    });
  }

  var cy = window.cy = cytoscape({
      container: container,
      style: stylesheet,
      layout: $_network.defaultLayout(data.nodes.length),
      elements: cytoElements,
    });

  cy.on('tap', 'node', function(evt){
    evt.preventDefault();
    console.log(evt.target.id());
    $_network.isolateNode(cy, evt.target.id());
    $(".collapse.peerInfo").collapse("hide");
    $("#peerInfo-" + evt.target.id()).collapse("show");
  });

  cy.on('tap', function(event){
    var evtTarget = event.target;
    if( evtTarget === cy ){ // tap on background
      $_network.showAll(cy);
      window.location.hash = "";
      $(".collapse.peerInfo").collapse("hide");
    }
  });

  return cy;
}
