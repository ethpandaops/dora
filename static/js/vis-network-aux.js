function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

$_network = {};

// Auxiliar function to fit a network to the screen
$_network.fitToScreen = function (network) {
  var options = {
    offset: { x: 0, y: 0 },
    duration: 1000,
    easingFunction: "easeInOutQuad",
  };
  network.fit({ animation: options });
}

// Default options for a network
$_network.defaultOptions = {
  layout: {
    randomSeed: 1
  },
  interaction: {
    hover: true
  },
  manipulation: {
    enabled: false,
  },
  groups: {
    internal: {
      shape: "dot",
      size: 30,
      font: {
        size: 16,
        face: "Tahoma",
        color: "#ffffff",
      },
      borderWidth: 3,
      shadow: true,
      color:{
        border: "#0077B6",
        background: "#fafafa",
        highlight: {
          border: "#E77D22",
          background: "#fafafa",
        },
        hover: {
          border: "#0077B6",
          background: "#fefefe",
        },
      }
    },
    external: {
      shape: "dot",
      shapeProperties: {
        borderDashes: [5, 5],
      },
      size: 20,
      font: {
        size: 14,
        face: "Tahoma",
        color: "#ffffff",
      },
      borderWidth: 2,
      shadow: true,
      color:{
        border: "#809FFF",
        background: "#fafafa",
        highlight: {
          border: "#E77D22",
          background: "#fafafa",
        },
        hover: {
          border: "#809FFF",
          background: "#fefefe",
        },
      }
    },
  },
  nodes: {
    shapeProperties: {
      interpolation: false    // 'true' for intensive zooming
    },
    shape: "dot",
    size: 30,
    font: {
      size: 16,
      face: "Tahoma",
      color: "#ffffff",
    },
    borderWidth: 2,
    shadow: true,
    color:{
      border: "#222222",
      background: "#666666",
      highlight: {
        border: "#E77D22",
        background: "#E77D22",
      },
      hover: {
        border: "#222222",
        background: "#666666",
      },
    }
  },
  edges: {
    arrows: {
      to: { enabled: true, scaleFactor: 1, type: "arrow" },
    },
    width: 2,
    shadow: true,
    color: {
      color: "#0077B6",
      highlight: "#E77D22",
      hover: "#0077B6",
      opacity: 0.7,
    },
    smooth: {
      type: "dynamic", // might need to change to "continuous" when there are too many nodes
      //type: "continuous",
    },
  },
  physics: {
    stabilization: false,
    barnesHut: {
      gravitationalConstant: -5000,
      springConstant: 0.001,
      springLength: 200,
    },
  },
};
