// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {TaskEscrowFactory} from "../src/TaskEscrowFactory.sol";
import {TaskEscrow} from "../src/TaskEscrow.sol";
import {IERC20} from "../src/interfaces/IERC20.sol";
import {IEIP3009} from "../src/interfaces/IEIP3009.sol";

/// @dev ERC20 with EIP-3009 receiveWithAuthorization support for testing.
/// Validates the authorization signature using ecrecover (matching real USDC behavior).
contract MockEIP3009Token is IERC20, IEIP3009 {
    string public name;
    string public symbol;
    uint8 public decimals;
    uint256 public totalSupply;
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;
    mapping(bytes32 => bool) public authorizationUsed;

    bytes32 public constant RECEIVE_WITH_AUTHORIZATION_TYPEHASH = keccak256(
        "ReceiveWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"
    );
    bytes32 public DOMAIN_SEPARATOR;

    constructor(string memory _name, string memory _symbol, uint8 _decimals) {
        name = _name;
        symbol = _symbol;
        decimals = _decimals;
        DOMAIN_SEPARATOR = keccak256(
            abi.encode(
                keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"),
                keccak256(bytes(_name)),
                keccak256(bytes("1")),
                block.chainid,
                address(this)
            )
        );
    }

    function mint(address to, uint256 amount) external {
        balanceOf[to] += amount;
        totalSupply += amount;
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        require(balanceOf[msg.sender] >= amount, "insufficient balance");
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
        return true;
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        return true;
    }

    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        require(balanceOf[from] >= amount, "insufficient balance");
        require(allowance[from][msg.sender] >= amount, "insufficient allowance");
        balanceOf[from] -= amount;
        allowance[from][msg.sender] -= amount;
        balanceOf[to] += amount;
        return true;
    }

    function receiveWithAuthorization(
        address from,
        address to,
        uint256 value,
        uint256 validAfter,
        uint256 validBefore,
        bytes32 nonce,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external {
        require(to == msg.sender, "caller must be the payee");
        require(block.timestamp > validAfter, "authorization not yet valid");
        require(block.timestamp < validBefore, "authorization expired");
        require(!authorizationUsed[nonce], "authorization already used");

        bytes32 structHash =
            keccak256(abi.encode(RECEIVE_WITH_AUTHORIZATION_TYPEHASH, from, to, value, validAfter, validBefore, nonce));
        bytes32 digest = keccak256(abi.encodePacked("\x19\x01", DOMAIN_SEPARATOR, structHash));
        address recovered = ecrecover(digest, v, r, s);
        require(recovered != address(0) && recovered == from, "invalid authorization");

        authorizationUsed[nonce] = true;
        require(balanceOf[from] >= value, "insufficient balance");
        balanceOf[from] -= value;
        balanceOf[to] += value;
    }
}

/// @dev Fee-on-transfer variant of MockEIP3009Token for testing InsufficientReceived.
contract MockFeeOnTransferEIP3009Token is IERC20, IEIP3009 {
    string public name;
    string public symbol;
    uint8 public decimals;
    uint256 public totalSupply;
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;

    bytes32 public constant RECEIVE_WITH_AUTHORIZATION_TYPEHASH = keccak256(
        "ReceiveWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"
    );
    bytes32 public DOMAIN_SEPARATOR;

    constructor(string memory _name, string memory _symbol, uint8 _decimals) {
        name = _name;
        symbol = _symbol;
        decimals = _decimals;
        DOMAIN_SEPARATOR = keccak256(
            abi.encode(
                keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"),
                keccak256(bytes(_name)),
                keccak256(bytes("1")),
                block.chainid,
                address(this)
            )
        );
    }

    function mint(address to, uint256 amount) external {
        balanceOf[to] += amount;
        totalSupply += amount;
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        require(balanceOf[msg.sender] >= amount, "insufficient balance");
        uint256 fee = amount / 100;
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount - fee;
        return true;
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        return true;
    }

    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        require(balanceOf[from] >= amount, "insufficient balance");
        require(allowance[from][msg.sender] >= amount, "insufficient allowance");
        uint256 fee = amount / 100;
        balanceOf[from] -= amount;
        allowance[from][msg.sender] -= amount;
        balanceOf[to] += amount - fee;
        return true;
    }

    /// @dev Takes a 1% fee during receiveWithAuthorization to simulate fee-on-transfer.
    function receiveWithAuthorization(
        address from,
        address to,
        uint256 value,
        uint256 validAfter,
        uint256 validBefore,
        bytes32,
        uint8,
        bytes32,
        bytes32
    ) external {
        require(to == msg.sender, "caller must be the payee");
        require(block.timestamp > validAfter, "authorization not yet valid");
        require(block.timestamp < validBefore, "authorization expired");
        require(balanceOf[from] >= value, "insufficient balance");

        uint256 fee = value / 100;
        balanceOf[from] -= value;
        balanceOf[to] += value - fee;
    }
}

contract TaskEscrowEIP3009Test is Test {
    TaskEscrowFactory internal factory;
    MockEIP3009Token internal usdc;

    uint256 internal buyerPk;
    address internal buyer;
    address internal worker = makeAddr("worker");
    address internal verifier = makeAddr("verifier");
    address internal arbitrator = makeAddr("arbitrator");
    address internal treasury = makeAddr("treasury");
    address internal owner = makeAddr("owner");
    address internal relayer = makeAddr("relayer");

    uint256 internal constant AMOUNT = 1000e6;
    uint16 internal constant FEE_BPS = 100;
    uint64 internal constant REVIEW = 86_400;
    uint64 internal constant DISPUTE = 172_800;
    uint64 internal constant ARB_TIMEOUT = 7 days;

    function setUp() public {
        (buyer, buyerPk) = makeAddrAndKey("buyer");
        factory = new TaskEscrowFactory(FEE_BPS, treasury, owner);
        usdc = new MockEIP3009Token("USD Coin", "USDC", 6);
        usdc.mint(buyer, 10_000e6);
    }

    function _createEscrow() internal returns (TaskEscrow) {
        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(usdc),
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
        return TaskEscrow(addr);
    }

    function _createETHEscrow() internal returns (TaskEscrow) {
        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: 1 ether,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(0),
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
        return TaskEscrow(addr);
    }

    function _signAuthorization(TaskEscrow escrow, bytes32 nonce, uint256 validAfter, uint256 validBefore)
        internal
        view
        returns (uint8 v, bytes32 r, bytes32 s)
    {
        bytes32 structHash = keccak256(
            abi.encode(
                usdc.RECEIVE_WITH_AUTHORIZATION_TYPEHASH(),
                buyer,
                address(escrow),
                AMOUNT,
                validAfter,
                validBefore,
                nonce
            )
        );
        bytes32 digest = keccak256(abi.encodePacked("\x19\x01", usdc.DOMAIN_SEPARATOR(), structHash));
        (v, r, s) = vm.sign(buyerPk, digest);
    }

    // ── Happy path: third-party relayer submits authorization ──

    function testFundWithAuthorization_HappyPath() public {
        TaskEscrow escrow = _createEscrow();
        bytes32 nonce = keccak256("nonce1");
        uint256 validAfter = 0;
        uint256 validBefore = block.timestamp + 1 hours;

        (uint8 v, bytes32 r, bytes32 s) = _signAuthorization(escrow, nonce, validAfter, validBefore);

        vm.prank(relayer);
        escrow.fundWithAuthorization(buyer, validAfter, validBefore, nonce, v, r, s);

        assertEq(uint8(escrow.status()), uint8(TaskEscrow.Status.Funded));
        assertEq(usdc.balanceOf(address(escrow)), AMOUNT);
        assertEq(usdc.balanceOf(buyer), 10_000e6 - AMOUNT);
    }

    function testFundWithAuthorization_BuyerSubmitsSelf() public {
        TaskEscrow escrow = _createEscrow();
        bytes32 nonce = keccak256("nonce2");
        uint256 validAfter = 0;
        uint256 validBefore = block.timestamp + 1 hours;

        (uint8 v, bytes32 r, bytes32 s) = _signAuthorization(escrow, nonce, validAfter, validBefore);

        vm.prank(buyer);
        escrow.fundWithAuthorization(buyer, validAfter, validBefore, nonce, v, r, s);

        assertEq(uint8(escrow.status()), uint8(TaskEscrow.Status.Funded));
    }

    function testFundWithAuthorization_EmitsEvent() public {
        TaskEscrow escrow = _createEscrow();
        bytes32 nonce = keccak256("nonce3");
        uint256 validAfter = 0;
        uint256 validBefore = block.timestamp + 1 hours;

        (uint8 v, bytes32 r, bytes32 s) = _signAuthorization(escrow, nonce, validAfter, validBefore);

        vm.expectEmit(true, false, false, true, address(escrow));
        emit TaskEscrow.EscrowFunded(buyer, AMOUNT);

        vm.prank(relayer);
        escrow.fundWithAuthorization(buyer, validAfter, validBefore, nonce, v, r, s);
    }

    // ── Revert: from != buyer ──

    function testFundWithAuthorization_RevertNotBuyer() public {
        TaskEscrow escrow = _createEscrow();
        bytes32 nonce = keccak256("nonce4");

        vm.expectRevert(TaskEscrow.Unauthorized.selector);
        vm.prank(relayer);
        escrow.fundWithAuthorization(worker, 0, block.timestamp + 1 hours, nonce, 27, bytes32(0), bytes32(0));
    }

    // ── Revert: wrong state (already funded) ──

    function testFundWithAuthorization_RevertAlreadyFunded() public {
        TaskEscrow escrow = _createEscrow();
        bytes32 nonce1 = keccak256("nonce5");
        uint256 validBefore = block.timestamp + 1 hours;

        (uint8 v, bytes32 r, bytes32 s) = _signAuthorization(escrow, nonce1, 0, validBefore);
        vm.prank(relayer);
        escrow.fundWithAuthorization(buyer, 0, validBefore, nonce1, v, r, s);

        bytes32 nonce2 = keccak256("nonce6");
        (v, r, s) = _signAuthorization(escrow, nonce2, 0, validBefore);

        vm.expectRevert(TaskEscrow.InvalidState.selector);
        vm.prank(relayer);
        escrow.fundWithAuthorization(buyer, 0, validBefore, nonce2, v, r, s);
    }

    // ── Revert: ETH escrow ──

    function testFundWithAuthorization_RevertETHEscrow() public {
        TaskEscrow escrow = _createETHEscrow();

        vm.expectRevert(TaskEscrow.ETHNotAccepted.selector);
        vm.prank(relayer);
        escrow.fundWithAuthorization(buyer, 0, block.timestamp + 1 hours, keccak256("n"), 27, bytes32(0), bytes32(0));
    }

    // ── Revert: fee-on-transfer (InsufficientReceived) ──

    function testFundWithAuthorization_RevertFeeOnTransfer() public {
        MockFeeOnTransferEIP3009Token feeToken = new MockFeeOnTransferEIP3009Token("FEE", "FEE", 6);
        feeToken.mint(buyer, 10_000e6);

        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(feeToken),
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                milestones: new TaskEscrowFactory.CreateMilestoneParams[](0)
            })
        );
        TaskEscrow escrow = TaskEscrow(addr);

        vm.expectRevert(TaskEscrow.InsufficientReceived.selector);
        vm.prank(relayer);
        escrow.fundWithAuthorization(buyer, 0, block.timestamp + 1 hours, keccak256("n"), 27, bytes32(0), bytes32(0));
    }

    // ── Integration: fund via authorization, then full lifecycle ──

    function testFundWithAuthorization_ThenSubmitAndApprove() public {
        TaskEscrow escrow = _createEscrow();
        bytes32 nonce = keccak256("lifecycle");
        uint256 validBefore = block.timestamp + 1 hours;

        (uint8 v, bytes32 r, bytes32 s) = _signAuthorization(escrow, nonce, 0, validBefore);
        vm.prank(relayer);
        escrow.fundWithAuthorization(buyer, 0, validBefore, nonce, v, r, s);

        assertEq(uint8(escrow.status()), uint8(TaskEscrow.Status.Funded));

        vm.prank(worker);
        escrow.submit(keccak256("result"), "ipfs://result");
        assertEq(uint8(escrow.status()), uint8(TaskEscrow.Status.Submitted));

        vm.prank(buyer);
        escrow.approveByBuyer();
        // approveByBuyer triggers auto-settlement when no verifier review is pending
        assertTrue(
            uint8(escrow.status()) == uint8(TaskEscrow.Status.Approved)
                || uint8(escrow.status()) == uint8(TaskEscrow.Status.Settled)
        );
    }

    // ── Integration: fund via authorization with milestone escrow ──

    function testFundWithAuthorization_MilestoneEscrow() public {
        TaskEscrowFactory.CreateMilestoneParams[] memory ms = new TaskEscrowFactory.CreateMilestoneParams[](2);
        ms[0] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 600e6, submissionDeadline: uint64(block.timestamp + 3 days)
        });
        ms[1] = TaskEscrowFactory.CreateMilestoneParams({
            amount: 400e6, submissionDeadline: uint64(block.timestamp + 7 days)
        });

        (, address addr) = factory.createEscrow(
            TaskEscrowFactory.CreateParams({
                buyer: buyer,
                worker: worker,
                verifier: verifier,
                arbitrator: arbitrator,
                amount: AMOUNT,
                workerStake: 0,
                submissionDeadline: uint64(block.timestamp + 7 days),
                reviewPeriodSeconds: REVIEW,
                disputePeriodSeconds: DISPUTE,
                taskSpecHash: keccak256("spec"),
                arbitratorTimeoutSeconds: ARB_TIMEOUT,
                token: address(usdc),
                backupWorker: address(0),
                backupDeadlineExtension: 0,
                milestones: ms
            })
        );
        TaskEscrow escrow = TaskEscrow(addr);

        bytes32 nonce = keccak256("ms-nonce");
        uint256 validBefore = block.timestamp + 1 hours;
        (uint8 v, bytes32 r, bytes32 s) = _signAuthorization(escrow, nonce, 0, validBefore);

        vm.prank(relayer);
        escrow.fundWithAuthorization(buyer, 0, validBefore, nonce, v, r, s);

        assertEq(uint8(escrow.status()), uint8(TaskEscrow.Status.Funded));
        assertEq(usdc.balanceOf(address(escrow)), AMOUNT);

        vm.prank(worker);
        escrow.submitMilestone(0, keccak256("ms0"), "ipfs://ms0");

        vm.prank(buyer);
        escrow.approveMilestoneByBuyer(0);
    }
}
