// SPDX-License-Identifier: MIT
pragma solidity ^0.8.15;

contract Whitelist {
    address public owner;

    mapping(address => bool) public whitelist;

    error InvalidOwner(address owner);

    error NotWhitelisted();

    modifier onlyOwner() {
        if (msg.sender != owner) {
            revert NotWhitelisted();
        }
        _;
    }

    constructor(address initialOwner) {
        if (initialOwner == address(0)) {
            revert InvalidOwner(initialOwner);
        }
        owner = initialOwner;
    }

    function addToWhitelist(address _address) public onlyOwner {
        whitelist[_address] = true;
    }

    function removeFromWhitelist(address _address) public onlyOwner {
        whitelist[_address] = false;
    }

    function isWhitelisted(address _address) public view returns (bool) {
        return whitelist[_address];
    }
}