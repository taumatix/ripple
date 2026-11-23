<!-- vendored 2026-11-23 from https://gerrit-review.googlesource.com/Documentation/cmd-stream-events.html; text extracted from the HTML, not edited -->



gerrit stream-events

gerrit stream-events

== NAME

version v3.15.0-rc4-356-g0bdf31d913 | Privacy | Terms,
gerrit stream-events - Monitor events occurring in real time.

Table of Contents

- SYNOPSIS

- DESCRIPTION

- ACCESS

- SCRIPTING

- OPTIONS

- EXAMPLES

- SCHEMA

- EVENTS

- Change Abandoned

- Change Deleted

- Change Merged

- Change Restored

- Comment Added

- Dropped Output

- Hashtags Changed

- Project Created

- Patchset Created

- Ref Updated

- Batch Ref Updated

- Reviewer Added

- Reviewer Deleted

- Topic Changed

- Work In Progress State Changed

- Private State Changed

- Vote Deleted

- Project Head Updated

- SEE ALSO

## SYNOPSIS

ssh -p <port> <host> gerrit stream-events

## DESCRIPTION

Provides a portal into the major events occurring on the server,
outputting activity data in real-time to the client.  Events are
filtered by the caller’s access permissions, ensuring the caller
only receives events for changes they can view on the web, or in
the project repository.

Event output is in JSON, one event per line.

## ACCESS

Caller must be a member of the privileged 'Administrators' group,
or have been granted
the 'Stream Events' global capability.

## SCRIPTING

This command is intended to be used in scripts.

## OPTIONS

--subscribe|-s

Type of the event to subscribe to.  Multiple --subscribe options
may be specified to subscribe to multiple events. When this option
is provided, only subscribed events are emitted and all other
events are ignored. When this option is omitted, all events are
emitted.

## EXAMPLES

$ ssh -p 29418 review.example.com gerrit stream-events
{"type":"comment-added",change:{"project":"tools/gerrit", ...}, ...}
{"type":"comment-added",change:{"project":"tools/gerrit", ...}, ...}

Only subscribe to specific event types:

$ ssh -p 29418 review.example.com gerrit stream-events \
   -s patchset-created -s ref-replicated

## SCHEMA

The JSON messages consist of nested objects referencing the change,
patchSet, account involved, and other attributes as appropriate.

Note that any field may be missing in the JSON messages, so consumers of
this JSON stream should deal with that appropriately.

## EVENTS

### Change Abandoned

Sent when a change has been abandoned.

type

"change-abandoned"

change

change attribute

patchSet

patchSet attribute

abandoner

account attribute

reason

Reason for abandoning the change.

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Change Deleted

Sent when a change has been deleted.

type

"change-deleted"

change

change attribute

deleter

account attribute

### Change Merged

Sent when a change has been merged into the git repository.

type

"change-merged"

change

change attribute

patchSet

patchSet attribute

submitter

account attribute

newRev

The state (revision) of the target branch after the operation that
closed the change was completed.

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Change Restored

Sent when an abandoned change has been restored.

type

"change-restored"

change

change attribute

patchSet

patchSet attribute

restorer

account attribute

reason

Reason for restoring the change.

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Comment Added

Sent when a review comment has been posted on a change.

type

"comment-added"

change

change attribute

patchSet

patchSet attribute

author

account attribute

approvals

All approval attributes granted.

comment

Review comment cover message.

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Dropped Output

Sent to notify a client that events have been dropped.

type

"dropped-output"

### Hashtags Changed

Sent when the hashtags have been added to or
removed from a change.

type

"hashtags-changed"

change

change attribute

editor

account attribute

added

List of hashtags added to the change

removed

List of hashtags removed from the change

hashtags

List of hashtags on the change after tags were added or removed

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Project Created

Sent when a new project has been created.

type

"project-created"

projectName

The created project name

projectHead

The created project head name

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Patchset Created

Sent when a new change has been uploaded, or a new patch set has been uploaded
to an existing change.

type

"patchset-created"

change

change attribute

patchSet

patchSet attribute

uploader

account attribute

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Ref Updated

Sent when a reference is updated in a git repository.

type

"ref-updated"

submitter

account attribute

refUpdate

refUpdate attribute

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Batch Ref Updated

Sent when a reference is updated in a git repository. A batch-ref-updated event contains all refs
updated in a single operation. Thus, the refUpdated-field contains a list of 1 (in case of a RefUpdate)
to n (in case of a BatchRefUpdate) ref-updates, i.e. listeners of batch-ref-updated events will be
notified about every ref update and not just about batch ref updates.
You may want to listen to individual or batch ref-updates, but not both of them. Listening to both
batch-ref-updates and ref-updates events will cause processing the same ref updates twice.

type

"batch-ref-updated"

submitter

account attribute

refUpdates

list of refUpdate attributes

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Reviewer Added

Sent when a reviewer is added to a change.

type

"reviewer-added"

change

change attribute

patchSet

patchSet attribute

reviewer

account attribute

adder

user that added the reviewer as account attribute

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Reviewer Deleted

Sent when a reviewer (with a vote) is removed from a change.

type

"reviewer-deleted"

change

change attribute

patchSet

patchSet attribute

reviewer

reviewer that was removed as account attribute

remover

user that removed the reviewer as account attribute

approvals

All approval attributes removed.

comment

Review comment cover message.

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Topic Changed

Sent when the topic of a change has been changed.

type

"topic-changed"

change

change attribute

changer

account attribute

oldTopic

Topic name before it was changed.

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Work In Progress State Changed

Sent when the WIP state of the change has changed.

type

wip-state-changed

change

change attribute

patchSet

patchSet attribute

changer

account attribute

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Private State Changed

Sent when the private state of the
change has changed.

type

private-state-changed

change

change attribute

patchSet

patchSet attribute

changer

account attribute

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

### Vote Deleted

Sent when a vote was removed from a change.

type

"vote-deleted"

change

change attribute

patchSet

patchSet attribute

reviewer

user whose vote was removed as account attribute

remover

user who removed the vote as account attribute

approvals

all votes as approval attributes

comment

Review comment cover message.

### Project Head Updated

Sent when project’s head is updated.

type

"project-head-updated"

oldHead

The old project head name

newHead

The new project head name

eventCreatedOn

Time in seconds since the UNIX epoch when this event was
created.

## SEE ALSO

- 

JSON Data Formats

- 

Access Controls

Part of Gerrit Code Review

  Search

Version v3.15.0-rc4-356-g0bdf31d913 | Privacy | Terms


