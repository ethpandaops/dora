const Web3 = require('web3');
const web3 = new Web3('http://127.0.0.1:32806'); // el-1-geth-lighthouse RPC

async function sendTransaction() {
  const accounts = [
    {
      address: "0x8943545177806ED17B9F23F0a21ee5948eCaa776",
      privateKey: "bcdf20249abf0ed6d944c0288fad489e33f66b3960d9e6229c1cd214ed3bbe31"
    },
    {
      address: "0xE25583099BA105D9ec0A67f5Ae86D90e50036425",
      privateKey: "39725efee3fb28614de3bacaffe4cc4bd8c436257e2c8bb887c4b5c4be45e76d"
    }
  ];

  try {
    const account = web3.eth.accounts.privateKeyToAccount(accounts[0].privateKey);
    web3.eth.accounts.wallet.add(account);
    
    const tx = {
      from: accounts[0].address,
      to: accounts[1].address,
      value: web3.utils.toWei('0.1', 'ether'),
      gas: 21000,
      gasPrice: await web3.eth.getGasPrice()
    };
    
    const receipt = await web3.eth.sendTransaction(tx);
    console.log('Transaction sent:', receipt.transactionHash);
    console.log('Block number:', receipt.blockNumber);
  } catch (error) {
    console.error('Error:', error.message);
  }
}

sendTransaction();
