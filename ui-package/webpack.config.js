const path = require('path');
const webpack = require('webpack');
const wpmerge = require('webpack-merge');
const MiniCssExtractPlugin = require("mini-css-extract-plugin");
const TerserPlugin = require("terser-webpack-plugin");
const Visualizer = require('webpack-visualizer-plugin2');
var pkgJson = require('./package.json');

var debug = !!process.env.DEBUG;

var webpackModuleConfigs = [
  {
    entry: './src/main',
    output: {
      path: path.join(__dirname, '/dist'),
      filename: 'react-ui.js',
      chunkFilename: 'react-ui-[name].js',
      publicPath: "/ui-package/",
    },
  },
];

var webpackBaseConfig = {
  mode: debug ? "development" : "production",
  devtool: "source-map",

  resolve: {
    extensions: [".tsx", ".ts", ".js", ".css", ".scss"],
  },
  target: ['web', 'es5'],

  module: {
    rules: [
      // babel-loader to load our jsx and tsx files
      {
        test: /\.(ts|js)x?$/,
        exclude: /node_modules/,
        use: {
          loader: 'babel-loader',
          options: {
            presets: [
              "@babel/preset-env",
              "@babel/preset-typescript",
              "@babel/preset-react"
            ],
            plugins: [
              "@babel/syntax-dynamic-import",
              "@babel/proposal-class-properties",
              "@babel/proposal-object-rest-spread",
              "@babel/plugin-syntax-flow"
            ]
          },
        },
      },
      {
        test: /\.(sa|sc|c)ss$/,
        use: [
          debug ? "style-loader" : MiniCssExtractPlugin.loader,
          "css-loader",
          "postcss-loader",
          "sass-loader",
        ],
      },
    ]
  },

  optimization: {
    minimize: debug ? false : true,
    minimizer: debug ? undefined : [
      new TerserPlugin({
        parallel: true,
        extractComments: {
          banner: '@dora-ui/submit-deposit: ' + JSON.stringify({
            version: pkgJson.version,
          }) + "\n",
        },
        terserOptions: {
          compress: true,
          keep_fnames: false,
          mangle: true,
          toplevel: true,
          module: true,
        }
      }),
    ],

    splitChunks: {
      cacheGroups: {
        walletconnectVendor: {
          test: /[\\/]node_modules[\\/](@walletconnect)[\\/]/,
          name: 'walletconnect',
          chunks: 'all',
          priority: 10,
        },
        rainbowkit: {
          test: /[\\/]node_modules[\\/](@rainbow-me\/rainbowkit)[\\/]/,
          name: 'rainbowkit',
          chunks: 'all',
          priority: 10,
        },
      },
    }
  },

  plugins: [
    new Visualizer({
      filename: 'webpack-stats.html',
      throwOnError: true
    }),
    new MiniCssExtractPlugin({
      filename: 'dora-ui.css',
      chunkFilename: 'dora-ui.[name].css',
    }),
  ]
};

module.exports = webpackModuleConfigs.map(function(moduleConfig) {
  return wpmerge.merge(webpackBaseConfig, moduleConfig);
});
