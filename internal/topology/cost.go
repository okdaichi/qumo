package topology

// Cost is the strongly-typed cost value used for edge weights.
// Currently used as uniform weight (1) in SDN topology.
// Designed to support future weighted edges (RTT, load, etc.).
type Cost float64
