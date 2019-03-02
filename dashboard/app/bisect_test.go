// Copyright 2019 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

// +build aetest

package dash

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/syzkaller/dashboard/dashapi"
	"github.com/google/syzkaller/pkg/email"
)

func TestBisectCause(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)
	crash := testCrash(build, 1)
	c.client2.ReportCrash(crash)
	c.client2.pollEmailBug()

	// No repro - no bisection.
	pollResp, _ := c.client2.JobPoll([]string{build.Manager})
	c.expectEQ(pollResp.ID, "")

	// Now upload 4 crashes with repros.
	crash2 := testCrashWithRepro(build, 2)
	c.client2.ReportCrash(crash2)
	msg2 := c.client2.pollEmailBug()

	// This is later, so will be bisected before the previous crash.
	c.advanceTime(time.Hour)
	crash3 := testCrashWithRepro(build, 3)
	c.client2.ReportCrash(crash3)
	c.client2.pollEmailBug()

	// This does not have C repro, so will be bisected after the previous ones.
	c.advanceTime(time.Hour)
	crash4 := testCrashWithRepro(build, 4)
	crash4.ReproC = nil
	c.client2.ReportCrash(crash4)
	msg4 := c.client2.pollEmailBug()

	// This is from a different manager, so won't be bisected.
	c.advanceTime(time.Hour)
	build2 := testBuild(2)
	c.client2.UploadBuild(build2)
	crash5 := testCrashWithRepro(build2, 5)
	c.client2.ReportCrash(crash5)
	c.client2.pollEmailBug()

	pollResp, _ = c.client2.JobPoll([]string{build.Manager})
	c.expectNE(pollResp.ID, "")
	c.expectEQ(pollResp.Type, dashapi.JobBisectCause)
	c.expectEQ(pollResp.Manager, build.Manager)
	c.expectEQ(pollResp.KernelConfig, build.KernelConfig)
	c.expectEQ(pollResp.SyzkallerCommit, build.SyzkallerCommit)
	c.expectEQ(pollResp.ReproOpts, []byte("repro opts 3"))
	c.expectEQ(pollResp.ReproSyz, []byte("syncfs(3)"))
	c.expectEQ(pollResp.ReproC, []byte("int main() { return 3; }"))

	// Since we did not reply, we should get the same response.
	pollResp2, _ := c.client2.JobPoll([]string{build.Manager})
	c.expectEQ(pollResp, pollResp2)

	// Bisection failed with an error.
	done := &dashapi.JobDoneReq{
		ID:    pollResp.ID,
		Log:   []byte("bisect log 3"),
		Error: []byte("bisect error 3"),
	}
	c.expectOK(c.client2.JobDone(done))

	// Now we should get bisect for crash 2.
	pollResp, _ = c.client2.JobPoll([]string{build.Manager})
	c.expectNE(pollResp.ID, pollResp2.ID)
	c.expectEQ(pollResp.ReproOpts, []byte("repro opts 2"))

	// Bisection succeeded.
	jobID := pollResp.ID
	done = &dashapi.JobDoneReq{
		ID:          jobID,
		Build:       *build,
		Log:         []byte("bisect log 2"),
		CrashTitle:  "bisect crash title",
		CrashLog:    []byte("bisect crash log"),
		CrashReport: []byte("bisect crash report"),
		Commits: []dashapi.Commit{
			{
				Hash:       "36e65cb4a0448942ec316b24d60446bbd5cc7827",
				Title:      "kernel: add a bug",
				Author:     "author@kernel.org",
				AuthorName: "Author Kernelov",
				CC: []string{
					"reviewer1@kernel.org", "\"Reviewer2\" <reviewer2@kernel.org>",
					// These must be filtered out:
					"syzbot@testapp.appspotmail.com",
					"syzbot+1234@testapp.appspotmail.com",
					"\"syzbot\" <syzbot+1234@testapp.appspotmail.com>",
				},
				Date: time.Date(2000, 2, 9, 4, 5, 6, 7, time.UTC),
			},
		},
	}
	done.Build.ID = jobID
	c.expectOK(c.client2.JobDone(done))

	_, extBugID, err := email.RemoveAddrContext(msg2.Sender)
	c.expectOK(err)
	_, dbCrash, _ := c.loadBug(extBugID)
	reproSyzLink := externalLink(c.ctx, textReproSyz, dbCrash.ReproSyz)
	reproCLink := externalLink(c.ctx, textReproC, dbCrash.ReproC)
	dbJob, dbBuild, dbJobCrash := c.loadJob(jobID)
	kernelConfigLink := externalLink(c.ctx, textKernelConfig, dbBuild.KernelConfig)
	bisectCrashReportLink := externalLink(c.ctx, textCrashReport, dbJob.CrashReport)
	bisectCrashLogLink := externalLink(c.ctx, textCrashLog, dbJob.CrashLog)
	bisectLogLink := externalLink(c.ctx, textLog, dbJob.Log)
	crashLogLink := externalLink(c.ctx, textCrashLog, dbJobCrash.Log)

	{
		msg := c.pollEmailBug()
		// Not mailed to commit author/cc because !MailMaintainers.
		c.expectEQ(msg.To, []string{"test@syzkaller.com"})
		c.expectEQ(msg.Subject, crash2.Title)
		c.expectEQ(len(msg.Attachments), 0)
		c.expectEQ(msg.Body, fmt.Sprintf(`syzbot has bisected this bug to:

commit 36e65cb4a0448942ec316b24d60446bbd5cc7827
Author: Author Kernelov <author@kernel.org>
Date:   Wed Feb 9 04:05:06 2000 +0000

    kernel: add a bug

bisection log:  %[2]v
start commit:   11111111 kernel_commit_title1
git tree:       repo1 branch1
final crash:    %[3]v
console output: %[4]v
kernel config:  %[5]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
syz repro:      %[6]v
C reproducer:   %[7]v

Reported-by: syzbot+%[1]v@testapp.appspotmail.com
Fixes: 36e65cb4 ("kernel: add a bug")
`, extBugID, bisectLogLink, bisectCrashReportLink, bisectCrashLogLink, kernelConfigLink, reproSyzLink, reproCLink))

		syzRepro := []byte(fmt.Sprintf("%s#%s\n%s", syzReproPrefix, crash2.ReproOpts, crash2.ReproSyz))
		c.checkURLContents(bisectLogLink, []byte("bisect log 2"))
		c.checkURLContents(bisectCrashReportLink, []byte("bisect crash report"))
		c.checkURLContents(bisectCrashLogLink, []byte("bisect crash log"))
		c.checkURLContents(kernelConfigLink, []byte("config1"))
		c.checkURLContents(reproSyzLink, syzRepro)
		c.checkURLContents(reproCLink, crash2.ReproC)
	}

	// The next reporting must get bug report with bisection results.
	c.incomingEmail(msg2.Sender, "#syz upstream")
	{
		msg := c.pollEmailBug()
		_, extBugID2, err := email.RemoveAddrContext(msg.Sender)
		c.expectOK(err)

		c.expectEQ(msg.To, []string{
			"author@kernel.org",
			"bugs@syzkaller.com",
			"default@maintainers.com",
			"reviewer1@kernel.org",
			"reviewer2@kernel.org",
		})
		c.expectEQ(msg.Subject, crash2.Title)
		c.expectEQ(msg.Body, fmt.Sprintf(`Hello,

syzbot found the following crash on:

HEAD commit:    11111111 kernel_commit_title1
git tree:       repo1 branch1
console output: %[2]v
kernel config:  %[3]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
compiler:       compiler1
syz repro:      %[4]v
C reproducer:   %[5]v
CC:             [author@kernel.org reviewer1@kernel.org reviewer2@kernel.org]

The bug was bisected to:

commit 36e65cb4a0448942ec316b24d60446bbd5cc7827
Author: Author Kernelov <author@kernel.org>
Date:   Wed Feb 9 04:05:06 2000 +0000

    kernel: add a bug

bisection log:  %[6]v
final crash:    %[7]v
console output: %[8]v

IMPORTANT: if you fix the bug, please add the following tag to the commit:
Reported-by: syzbot+%[1]v@testapp.appspotmail.com
Fixes: 36e65cb4 ("kernel: add a bug")

report2

---
This bug is generated by a bot. It may contain errors.
See https://goo.gl/tpsmEJ for more information about syzbot.
syzbot engineers can be reached at syzkaller@googlegroups.com.

syzbot will keep track of this bug report. See:
https://goo.gl/tpsmEJ#bug-status-tracking for how to communicate with syzbot.
syzbot can test patches for this bug, for details see:
https://goo.gl/tpsmEJ#testing-patches`,
			extBugID2, crashLogLink, kernelConfigLink, reproSyzLink, reproCLink,
			bisectLogLink, bisectCrashReportLink, bisectCrashLogLink))
	}

	// Crash 4 is bisected in reporting with MailMaintainers.
	c.incomingEmail(msg4.Sender, "#syz upstream")
	msg4 = c.pollEmailBug()
	pollResp, _ = c.client2.JobPoll([]string{build.Manager})

	// Bisection succeeded.
	jobID = pollResp.ID
	done = &dashapi.JobDoneReq{
		ID:          jobID,
		Build:       *build,
		Log:         []byte("bisect log 4"),
		CrashTitle:  "bisect crash title 4",
		CrashLog:    []byte("bisect crash log 4"),
		CrashReport: []byte("bisect crash report 4"),
		Commits: []dashapi.Commit{
			{
				Hash:       "36e65cb4a0448942ec316b24d60446bbd5cc7827",
				Title:      "kernel: add a bug",
				Author:     "author@kernel.org",
				AuthorName: "Author Kernelov",
				CC: []string{
					"reviewer1@kernel.org", "\"Reviewer2\" <reviewer2@kernel.org>",
					// These must be filtered out:
					"syzbot@testapp.appspotmail.com",
					"syzbot+1234@testapp.appspotmail.com",
					"\"syzbot\" <syzbot+1234@testapp.appspotmail.com>",
				},
				Date: time.Date(2000, 2, 9, 4, 5, 6, 7, time.UTC),
			},
		},
	}
	done.Build.ID = jobID
	c.expectOK(c.client2.JobDone(done))

	{
		msg := c.pollEmailBug()
		c.expectEQ(msg.Subject, crash4.Title)
		c.expectEQ(msg.To, []string{
			"author@kernel.org",
			"bugs@syzkaller.com",
			"default@maintainers.com",
			"reviewer1@kernel.org",
			"reviewer2@kernel.org",
		})
	}

	// No more bisection jobs.
	pollResp, _ = c.client2.JobPoll([]string{build.Manager})
	c.expectEQ(pollResp.ID, "")
}

func TestBisectCauseInconclusive(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)
	crash := testCrashWithRepro(build, 1)
	c.client2.ReportCrash(crash)
	msg := c.client2.pollEmailBug()

	pollResp, err := c.client2.JobPoll([]string{build.Manager})
	c.expectOK(err)
	jobID := pollResp.ID
	done := &dashapi.JobDoneReq{
		ID:    jobID,
		Build: *build,
		Log:   []byte("bisect log"),
		Commits: []dashapi.Commit{
			{
				Hash:       "111111111111111111111111",
				Title:      "kernel: break build",
				Author:     "hacker@kernel.org",
				AuthorName: "Hacker Kernelov",
				CC:         []string{"reviewer1@kernel.org", "reviewer2@kernel.org"},
				Date:       time.Date(2000, 2, 9, 4, 5, 6, 7, time.UTC),
			},
			{
				Hash:       "222222222222222222222222",
				Title:      "kernel: now add a bug to the broken build",
				Author:     "author@kernel.org",
				AuthorName: "Author Kernelov",
				CC:         []string{"reviewer3@kernel.org", "reviewer4@kernel.org"},
				Date:       time.Date(2001, 2, 9, 4, 5, 6, 7, time.UTC),
			},
		},
	}
	done.Build.ID = jobID
	c.expectOK(c.client2.JobDone(done))

	_, extBugID, err := email.RemoveAddrContext(msg.Sender)
	c.expectOK(err)
	_, dbCrash, _ := c.loadBug(extBugID)
	reproSyzLink := externalLink(c.ctx, textReproSyz, dbCrash.ReproSyz)
	reproCLink := externalLink(c.ctx, textReproC, dbCrash.ReproC)
	dbJob, dbBuild, dbJobCrash := c.loadJob(jobID)
	kernelConfigLink := externalLink(c.ctx, textKernelConfig, dbBuild.KernelConfig)
	bisectLogLink := externalLink(c.ctx, textLog, dbJob.Log)
	crashLogLink := externalLink(c.ctx, textCrashLog, dbJobCrash.Log)

	{
		msg := c.pollEmailBug()
		// Not mailed to commit author/cc because !MailMaintainers.
		c.expectEQ(msg.To, []string{"test@syzkaller.com"})
		c.expectEQ(msg.Subject, crash.Title)
		c.expectEQ(len(msg.Attachments), 0)
		c.expectEQ(msg.Body, fmt.Sprintf(`Bisection is inconclusive: the first bad commit could be any of:

11111111 kernel: break build
22222222 kernel: now add a bug to the broken build

bisection log:  %[2]v
start commit:   11111111 kernel_commit_title1
git tree:       repo1 branch1
kernel config:  %[3]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
syz repro:      %[4]v
C reproducer:   %[5]v

`, extBugID, bisectLogLink, kernelConfigLink, reproSyzLink, reproCLink))
	}

	// The next reporting must get bug report with bisection results.
	c.incomingEmail(msg.Sender, "#syz upstream")
	{
		msg := c.pollEmailBug()
		_, extBugID2, err := email.RemoveAddrContext(msg.Sender)
		c.expectOK(err)
		c.expectEQ(msg.To, []string{
			"bugs@syzkaller.com",
			"default@maintainers.com",
		})
		c.expectEQ(msg.Body, fmt.Sprintf(`Hello,

syzbot found the following crash on:

HEAD commit:    11111111 kernel_commit_title1
git tree:       repo1 branch1
console output: %[2]v
kernel config:  %[3]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
compiler:       compiler1
syz repro:      %[4]v
C reproducer:   %[5]v

Bisection is inconclusive: the first bad commit could be any of:

11111111 kernel: break build
22222222 kernel: now add a bug to the broken build

bisection log:  %[6]v

IMPORTANT: if you fix the bug, please add the following tag to the commit:
Reported-by: syzbot+%[1]v@testapp.appspotmail.com

report1

---
This bug is generated by a bot. It may contain errors.
See https://goo.gl/tpsmEJ for more information about syzbot.
syzbot engineers can be reached at syzkaller@googlegroups.com.

syzbot will keep track of this bug report. See:
https://goo.gl/tpsmEJ#bug-status-tracking for how to communicate with syzbot.
syzbot can test patches for this bug, for details see:
https://goo.gl/tpsmEJ#testing-patches`,
			extBugID2, crashLogLink, kernelConfigLink, reproSyzLink, reproCLink, bisectLogLink))
	}
}

func TestBisectCauseAncient(t *testing.T) {
	c := NewCtx(t)
	defer c.Close()

	build := testBuild(1)
	c.client2.UploadBuild(build)
	crash := testCrashWithRepro(build, 1)
	c.client2.ReportCrash(crash)
	msg := c.client2.pollEmailBug()

	pollResp, err := c.client2.JobPoll([]string{build.Manager})
	c.expectOK(err)
	jobID := pollResp.ID
	done := &dashapi.JobDoneReq{
		ID:          jobID,
		Build:       *build,
		Log:         []byte("bisect log"),
		CrashTitle:  "bisect crash title",
		CrashLog:    []byte("bisect crash log"),
		CrashReport: []byte("bisect crash report"),
	}
	done.Build.ID = jobID
	c.expectOK(c.client2.JobDone(done))

	_, extBugID, err := email.RemoveAddrContext(msg.Sender)
	c.expectOK(err)
	_, dbCrash, _ := c.loadBug(extBugID)
	reproSyzLink := externalLink(c.ctx, textReproSyz, dbCrash.ReproSyz)
	reproCLink := externalLink(c.ctx, textReproC, dbCrash.ReproC)
	dbJob, dbBuild, dbJobCrash := c.loadJob(jobID)
	bisectCrashReportLink := externalLink(c.ctx, textCrashReport, dbJob.CrashReport)
	bisectCrashLogLink := externalLink(c.ctx, textCrashLog, dbJob.CrashLog)
	kernelConfigLink := externalLink(c.ctx, textKernelConfig, dbBuild.KernelConfig)
	bisectLogLink := externalLink(c.ctx, textLog, dbJob.Log)
	crashLogLink := externalLink(c.ctx, textCrashLog, dbJobCrash.Log)

	{
		msg := c.pollEmailBug()
		// Not mailed to commit author/cc because !MailMaintainers.
		c.expectEQ(msg.To, []string{"test@syzkaller.com"})
		c.expectEQ(msg.Subject, crash.Title)
		c.expectEQ(len(msg.Attachments), 0)
		c.expectEQ(msg.Body, fmt.Sprintf(`Bisection is inconclusive: the bug happens on the oldest tested release.

bisection log:  %[2]v
start commit:   11111111 kernel_commit_title1
git tree:       repo1 branch1
final crash:    %[3]v
console output: %[4]v
kernel config:  %[5]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
syz repro:      %[6]v
C reproducer:   %[7]v

`, extBugID, bisectLogLink, bisectCrashReportLink, bisectCrashLogLink,
			kernelConfigLink, reproSyzLink, reproCLink))
	}

	// The next reporting must get bug report with bisection results.
	c.incomingEmail(msg.Sender, "#syz upstream")
	{
		msg := c.pollEmailBug()
		_, extBugID2, err := email.RemoveAddrContext(msg.Sender)
		c.expectOK(err)
		c.expectEQ(msg.To, []string{
			"bugs@syzkaller.com",
			"default@maintainers.com",
		})
		c.expectEQ(msg.Body, fmt.Sprintf(`Hello,

syzbot found the following crash on:

HEAD commit:    11111111 kernel_commit_title1
git tree:       repo1 branch1
console output: %[2]v
kernel config:  %[3]v
dashboard link: https://testapp.appspot.com/bug?extid=%[1]v
compiler:       compiler1
syz repro:      %[4]v
C reproducer:   %[5]v

Bisection is inconclusive: the bug happens on the oldest tested release.

bisection log:  %[6]v
final crash:    %[7]v
console output: %[8]v

IMPORTANT: if you fix the bug, please add the following tag to the commit:
Reported-by: syzbot+%[1]v@testapp.appspotmail.com

report1

---
This bug is generated by a bot. It may contain errors.
See https://goo.gl/tpsmEJ for more information about syzbot.
syzbot engineers can be reached at syzkaller@googlegroups.com.

syzbot will keep track of this bug report. See:
https://goo.gl/tpsmEJ#bug-status-tracking for how to communicate with syzbot.
syzbot can test patches for this bug, for details see:
https://goo.gl/tpsmEJ#testing-patches`,
			extBugID2, crashLogLink, kernelConfigLink, reproSyzLink, reproCLink,
			bisectLogLink, bisectCrashReportLink, bisectCrashLogLink))
	}
}
