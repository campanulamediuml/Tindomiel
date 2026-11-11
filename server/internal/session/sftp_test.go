package session

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestSFTPClientIsSharedAcrossConcurrentRequests(t *testing.T) {
	sshClient := &ssh.Client{}
	remote := &sftp.Client{}
	var calls atomic.Int32
	session := &Session{
		client:     sshClient,
		status:     Ready,
		generation: 1,
		newSFTPClient: func(client *ssh.Client) (*sftp.Client, error) {
			if client != sshClient {
				t.Fatal("unexpected SSH client")
			}
			calls.Add(1)
			return remote, nil
		},
	}
	const requests = 20
	var workers sync.WaitGroup
	workers.Add(requests)
	for range requests {
		go func() {
			defer workers.Done()
			client, err := session.SFTPClient()
			if err != nil {
				t.Error(err)
				return
			}
			if client != remote {
				t.Error("request received a different SFTP client")
			}
		}()
	}
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("SFTP client created %d times, want 1", calls.Load())
	}
}
