package chain

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const defaultLogChunkSize = uint64(2000)

type Client struct {
	eth          *ethclient.Client
	key          *ecdsa.PrivateKey
	address      common.Address
	chainID      *big.Int
	mu           sync.Mutex
	nonce        uint64
	nonceOK      bool
	logChunkSize uint64
}

func NewClient(rpcURL, privateKeyHex string, chainID int64, opts ...ClientOption) (*Client, error) {
	if rpcURL == "" {
		// Allow nil client for testing/offline mode
		return &Client{chainID: big.NewInt(chainID), logChunkSize: defaultLogChunkSize}, nil
	}

	eth, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc: %w", err)
	}

	c := &Client{
		eth:          eth,
		chainID:      big.NewInt(chainID),
		logChunkSize: defaultLogChunkSize,
	}

	for _, opt := range opts {
		opt(c)
	}

	if privateKeyHex != "" {
		key, err := crypto.HexToECDSA(stripHexPrefix(privateKeyHex))
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		c.key = key
		c.address = crypto.PubkeyToAddress(key.PublicKey)
	}

	return c, nil
}

// ClientOption configures optional Client parameters.
type ClientOption func(*Client)

// WithLogChunkSize sets the maximum block range per eth_getLogs request.
// Use a small value (e.g. 9) for free-tier RPC providers with strict limits.
func WithLogChunkSize(size uint64) ClientOption {
	return func(c *Client) {
		if size > 0 {
			c.logChunkSize = size
		}
	}
}

func (c *Client) Address() common.Address {
	return c.address
}

func (c *Client) ChainID() *big.Int {
	return c.chainID
}

func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	if c.eth == nil {
		return 0, errors.New("chain client not connected (offline mode)")
	}
	return c.eth.BlockNumber(ctx)
}

func (c *Client) FilterLogs(ctx context.Context, addresses []common.Address, topics [][]common.Hash, fromBlock, toBlock uint64) ([]types.Log, error) {
	if c.eth == nil {
		return nil, errors.New("chain client not connected (offline mode)")
	}
	chunkSize := c.logChunkSize
	if chunkSize == 0 {
		chunkSize = defaultLogChunkSize
	}
	var all []types.Log
	for from := fromBlock; from <= toBlock; from += chunkSize {
		to := min(from+chunkSize-1, toBlock)
		query := ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(from),
			ToBlock:   new(big.Int).SetUint64(to),
			Addresses: addresses,
			Topics:    topics,
		}
		logs, err := c.eth.FilterLogs(ctx, query)
		if err != nil {
			return nil, err
		}
		all = append(all, logs...)
	}
	return all, nil
}

func (c *Client) SendTx(ctx context.Context, to common.Address, data []byte, value *big.Int) (*types.Transaction, error) {
	if c.eth == nil {
		return nil, errors.New("chain client not connected (offline mode)")
	}
	if c.key == nil {
		return nil, errors.New("no private key configured")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.sendTxLocked(ctx, to, data, value, true)
}

// sendTxLocked sends a transaction while the mutex is held. If allowRetry is true
// and the send fails with "nonce too low", it refreshes the nonce from the chain
// and retries once.
func (c *Client) sendTxLocked(ctx context.Context, to common.Address, data []byte, value *big.Int, allowRetry bool) (*types.Transaction, error) {
	if !c.nonceOK {
		nonce, err := c.eth.PendingNonceAt(ctx, c.address)
		if err != nil {
			return nil, fmt.Errorf("get nonce: %w", err)
		}
		c.nonce = nonce
		c.nonceOK = true
	}

	gasPrice, err := c.eth.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("suggest gas price: %w", err)
	}

	if value == nil {
		value = big.NewInt(0)
	}

	msg := ethereum.CallMsg{
		From:  c.address,
		To:    &to,
		Value: value,
		Data:  data,
	}
	gasLimit, err := c.eth.EstimateGas(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("estimate gas: %w", err)
	}

	tx := types.NewTransaction(c.nonce, to, value, gasLimit, gasPrice, data)
	signer := types.NewEIP155Signer(c.chainID)
	signedTx, err := types.SignTx(tx, signer, c.key)
	if err != nil {
		return nil, fmt.Errorf("sign tx: %w", err)
	}

	if err := c.eth.SendTransaction(ctx, signedTx); err != nil {
		errMsg := err.Error()
		// "already known" means the tx is already in the mempool — treat as success
		// rather than retrying with a fresh nonce (which would submit a different tx).
		if strings.Contains(errMsg, "already known") {
			c.nonce++
			c.nonceOK = true
			return signedTx, nil
		}
		if allowRetry && strings.Contains(errMsg, "nonce too low") {
			nonce, fetchErr := c.eth.PendingNonceAt(ctx, c.address)
			if fetchErr != nil {
				return nil, fmt.Errorf("send tx: %w (nonce refresh also failed: %w)", err, fetchErr)
			}
			c.nonce = nonce
			c.nonceOK = true
			return c.sendTxLocked(ctx, to, data, value, false)
		}
		return nil, fmt.Errorf("send tx: %w", err)
	}

	c.nonce++
	return signedTx, nil
}

func (c *Client) CallContract(ctx context.Context, to common.Address, data []byte) ([]byte, error) {
	if c.eth == nil {
		return nil, errors.New("chain client not connected (offline mode)")
	}
	msg := ethereum.CallMsg{
		To:   &to,
		Data: data,
	}
	return c.eth.CallContract(ctx, msg, nil)
}

func (c *Client) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	if c.eth == nil {
		return nil, errors.New("chain client not connected (offline mode)")
	}
	return c.eth.TransactionReceipt(ctx, txHash)
}

func stripHexPrefix(s string) string {
	if len(s) >= 2 && s[:2] == "0x" {
		return s[2:]
	}
	return s
}
