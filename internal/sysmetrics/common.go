package sysmetrics

import "net"

// netInterfaces is net.Interfaces behind a variable so a test can substitute a
// fixed list instead of whatever the build machine happens to have.
var netInterfaces = net.Interfaces
