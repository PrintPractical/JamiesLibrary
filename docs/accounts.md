# Accounts, permissions, invitations and recovery

This guide is for everyone who has an account (signing in, registering, changing your password) and for the people who look after the others' accounts (invitations, roles, permissions, deactivation, password resets), and it ends with what the person who runs the server does when the only administrator can't get in. The library has no email: passwords and codes are handed over in person or through a channel you trust.

- [Signing in and out](#signing-in-and-out)
- [Registering with an invitation code](#registering-with-an-invitation-code)
- [Changing your password](#changing-your-password)
- [Permissions and roles](#permissions-and-roles)
- [Inviting people](#inviting-people)
- [Managing people](#managing-people)
- [Rules that protect the library](#rules-that-protect-the-library)
- [Resetting someone's password](#resetting-someones-password)
- [Locked out: recovering the only administrator](#locked-out-recovering-the-only-administrator)
- [Messages and what to do](#messages-and-what-to-do)

## Signing in and out

Open the library and, if you aren't signed in, you arrive at **Sign in** (`/signin`). Enter your username (capitals don't matter) and your password. After signing in you go to the page you were heading for.

**Sign out** is on the **Account** page (`/account`). It ends the session on the server, not just in the browser. Each sign-in starts a new session.

- A session ends by itself after it has been unused for a while: 8 hours to begin with. An administrator sets this in Settings ([how long a session lasts](guide-admin.md#signing-in-how-long-a-session-lasts)). You are then asked to sign in again.
- Wrong sign-ins are counted by the address they came from, not by account. After 5 in a row from one address, that address is refused for 15 minutes ("Too many attempts, try again later."). Nobody's account is locked by this, and a correct sign-in by someone else at another address is not affected.
- A deactivated account sees "This account is deactivated. Ask a librarian or an administrator." only after giving the right password. Otherwise the message is the same as for a wrong password ("The username or password is wrong."), so it doesn't reveal which usernames exist.

## Registering with an invitation code

Nobody can make an account without a code from someone who may invite people, and a code works once.

1. Get a code, like `7V5N-7BYZ-K1WK-8HDB`, and the library's address from a librarian or administrator.
2. Open `/register` (the **Sign in** page links to it) and fill in the code, your name (how the library shows you), a username and a password.
3. Choose **Create account**. You are signed in at once with the role the code was made for.

Details:

- Capitals, spaces and hyphens in the code don't matter; `7v5n 7byz k1wk 8hdb` works.
- A username is 1 to 64 characters with no spaces or hidden characters, and is unique whatever its capitals. A name taken already gives "That username is taken." A display name is 1 to 64 characters.
- A password is at least 10 and at most 256 characters, and has no other rules: a few ordinary words make a good one. Shorter gives "A password must be at least 10 characters."
- A wrong, used or revoked code gives one message, "That invitation code isn't valid. Check it, or ask for a new one." A code also stops working if the person who made it is deactivated or no longer holds the permissions the code gives. After 10 wrong codes from one address, registration (and first-run setup) is refused from that address for 15 minutes.

## Changing your password

Open **Account**, enter your current password and a new one (at least 10 characters, different from the current one) and choose **Change password**. Your other sessions end; the one you are using carries on. A wrong current password counts like a wrong sign-in towards the address limit above.

When an administrator has reset your password, the library shows only **Choose a new password** until you have done this (see [Resetting someone's password](#resetting-someones-password)). You can also sign out from that page.

## Permissions and roles

What a person may do is a set of **16 permissions**. Every action in the library needs one, and the server checks it on every request, so a hidden button is never the only protection. A change to someone's permissions, or deactivating them, takes effect on their next request.

| Permission | Allows |
|---|---|
| `catalog.browse` | Search and view books and their copies |
| `loans.own.view` | See your own loans and history |
| `loans.own.renew` | Renew your own loans |
| `reviews.write` | Write ratings and reviews |
| `circ.self_checkout` | Check books out to yourself (when self-checkout is on) |
| `patrons.lookup` | Find patrons and see their loans |
| `circ.desk` | Check out, check in and renew for patrons |
| `cards.reissue` | Replace a patron's library card |
| `catalog.manage` | Add, change and remove books and copies |
| `reviews.remove` | Remove other people's reviews |
| `reports.view` | See statistics and the circulation report |
| `activity.view` | See the activity log |
| `settings.manage` | Change the library's name, look, rules and switches |
| `invitations.manage` | Make, see and revoke invitation codes |
| `users.manage` | List users, deactivate and reactivate them, reset their passwords |
| `users.permissions` | Change users' roles and switch their permissions |

A **role** is a ready-made set, a starting point:

| Role | Permissions |
|---|---|
| Patron | `catalog.browse`, `loans.own.view`, `loans.own.renew`, `reviews.write`, `circ.self_checkout` |
| Librarian | all of the patron's, and `patrons.lookup`, `circ.desk`, `cards.reissue`, `catalog.manage`, `reviews.remove` |
| Administrator | all 16 |

A person holds permissions; the role is only the preset last applied to them. An administrator can switch single permissions on or off for one person (a patron who may also remove reviews, say). The user list and the person's page then show **Differs from librarian preset** (or whichever role it is) so you can see who has been changed. Choosing a role again replaces all that person's permissions with the role's preset.

The side bar shows only the screens a person may use.

## Inviting people

Open **People & invites** in the side bar (`/admin/people`; it needs `invitations.manage`).

1. Under **Invitations**, choose the role the new account gets and **Create invitation**. The code appears in the list, newest first, with its role, who made it, its date and its status: Unused, Used or Revoked.
2. Hand the code over privately, with the library's address followed by `/register`. Whoever holds a librarian or administrator code can register with that role's permissions, so don't post one in a group chat or show it on a shared screen.
3. **Revoke** an unused code that you no longer want to be used. A code that has been used can't be revoked.

You can only invite to a role whose permissions you hold in full. Roles that would give more than you have aren't offered, and the code of such an invitation, made by someone else, is shown as "Code hidden: it gives permissions you don't hold" and can't be revoked by you. Without this, a librarian allowed to manage invitations could read an administrator's code and register as an administrator.

Codes are kept as made, so people who may manage invitations can see them again later. Invitations are recorded in the [activity log](guide-admin.md#the-activity-log) by role and creator, never with the code.

## Managing people

**People & invites** lists everyone, 25 to a page, with role, active or deactivated, and whether they differ from their role's preset (needs `users.manage`). Choose a person to open their page (`/admin/people/ID`):

- **Role** (needs `users.permissions`): pick a role and **Set role**. It replaces all of the person's permissions with the preset.
- **Permissions** (needs `users.permissions`): 16 switches, each saved when you flip it. A switch you can't use is explained under it.
- **Account** (needs `users.manage`): **Deactivate**, **Reactivate** and **Reset password**.

**Deactivating** someone signs them out at once and stops them signing in. Nothing is deleted: their loans and history stay, staff can still see them, and a book can't be checked out to them. **Reactivate** lets them sign in again with their old password.

Lowering someone's role or permissions also ends their sessions, so the change can't be sidestepped by a session already open. Changes of role, permissions and activation, and password resets, are written to the activity log with who did them and to whom.

## Rules that protect the library

These are enforced by the server, whatever the screen shows.

- **No escalation.** You can give someone a permission or role only if you hold every permission in it yourself. You can switch off, deactivate, reset or otherwise change another person's account only if everything they hold is something you hold too. So a librarian who was given `users.manage` still can't reset an administrator's password. Messages: "That person has permissions you don't have, so you can't change their account." and "You can't give a permission you don't have yourself."
- **The last administrator.** There must always be at least one active person who holds both `users.manage` and `users.permissions`. A change that would leave nobody (deactivating them, switching either off, lowering the role) is refused: "That would leave nobody able to manage users and their permissions. Make someone else an administrator first." The check and the change happen together, so two changes at once can't both get past it.
- **Your own account** follows the same rules. You can't give yourself a permission you don't have.

## Resetting someone's password

For someone who has forgotten theirs (there is no email, so this is how it's done). Needs `users.manage`, and the no-escalation rule applies.

1. Open the person's page, choose **Reset password** and confirm.
2. A **temporary password** of 16 letters and digits is shown once. Give it to the person in person or by a private message, then choose **I've passed it on**. It is never shown again; if it's lost, reset again.
3. The person's old password stops working and their sessions end. At their next sign-in they are held at **Choose a new password** until they set one.

The library stores only a hash of the temporary password. Don't put it anywhere that keeps a history, such as a shared chat or a ticket.

## Locked out: recovering the only administrator

If the only administrator has forgotten their password, or was deactivated or demoted by mistake, nobody on the web can fix it. The person who runs the server does it from the server itself, with a command that works straight on the data directory. There is no way to do it over the network.

Run it with the **same version of the program as the server is running**: it opens the data directory without the server's lock and brings the database up to date if it needs to.

**With Docker** (run from the folder with the compose file; use the same `-p` project name and file you installed with):

```sh
docker compose -p jamieslibrary -f deploy/compose.lan.yml exec jamieslibrary /usr/local/bin/jamieslibrary admin reset-password USERNAME
```

**Without Docker**, as the user the server runs as:

```sh
./jamieslibrary admin reset-password --data DIR USERNAME
```

`--data DIR` is the server's data directory; without it the command uses `JL_DATA_DIR`, then `./data`. Add `--make-admin` to also give the account the administrator role, for an owner who was demoted. A real run:

```
$ jamieslibrary admin reset-password owner
Temporary password for owner: F7kEH23gXB4imQSm
They must choose a new password at their first sign-in. All their sessions have ended.
```

```
$ jamieslibrary admin reset-password --make-admin pat
pat is now an administrator.
Temporary password for pat: UZ24HFfNdSw9XBzu
They must choose a new password at their first sign-in. All their sessions have ended.
```

Then:

1. Read the temporary password from the command's output and give it to the person. **It appears only there**: don't copy it into a log, a ticket or a chat, and clear the terminal when done. The server never logs it.
2. The person signs in with the username and the temporary password and is asked for a new password.
3. If their account was deactivated, the command says so (`Note: NAME is deactivated and can't sign in until an administrator reactivates them.`): reset does not reactivate it. Reactivate them from another administrator account, or, if there is none, make a different account an administrator with `--make-admin` first and use it.

The reset ends the person's sessions and is recorded in the [activity log](guide-admin.md#the-activity-log) as done by "Server console". If the username doesn't exist, the command says `there is no user named "NAME"` and changes nothing; if the data directory has no library it says `there is no library in the data directory "DIR" (no library.db)`. A mistake in the options prints `usage: jamieslibrary admin reset-password [--data DIR] [--make-admin] USERNAME`.

The command also works on someone who isn't an administrator (it then prints a note saying so), so it is the owner's tool, not a patron's: anyone who can run it can already read all of the data.

## Messages and what to do

| Message | Meaning and what to do |
|---|---|
| The username or password is wrong. | Check both; usernames ignore capitals, passwords don't. Forgotten: ask an administrator for a reset. |
| Too many attempts, try again later. | Five wrong sign-ins (or ten wrong invitation codes) from this address: wait 15 minutes. |
| This account is deactivated. Ask a librarian or an administrator. | An administrator can reactivate it. |
| Choose a new password before you do anything else. | Your password was reset: change it on the page shown. |
| That invitation code isn't valid. Check it, or ask for a new one. | Wrong, used or revoked, or its maker can no longer give that role. Ask for a new one. |
| That username is taken. | Choose another. |
| That person has permissions you don't have, so you can't change their account. | Ask someone who holds all of them. |
| You can't give a role with permissions you don't have yourself. | The same rule for roles and invitations. |
| That would leave nobody able to manage users and their permissions. Make someone else an administrator first. | The last-administrator rule. |
| The server is busy: try again in a moment. | Many password checks at once; try again. |
