package supervisor

import "os"

// getpid returns the current process pid. Lifted to its own file to make
// it cheap to substitute for tests.
func getpid() int { return os.Getpid() }
