package doge

import "fmt"

// ChainFromName returns the chain parameters for the given chain name
func ChainFromName(name string) (*ChainParams, error) {
	switch name {
	case "main":
		return &MainNetParams, nil
	case "test":
		return &TestNetParams, nil
	case "regtest":
		return &RegTestParams, nil
	default:
		return nil, fmt.Errorf("unknown chain name: %s", name)
	}
}
