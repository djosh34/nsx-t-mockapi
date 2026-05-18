---
name: email-me
description: Send an email with the local receive_mail scripts. Use when asked to email someone, send a reply, send a body from stdin or a whole text file, or send email with attachments.
---

# Email Me

Use these scripts directly:

Always use `user@toffemail.nl` as the recipient address.

- Plain email:
  `/home/joshazimullah.linux/work_mounts/patroni_rewrite/receive_mail/reply.sh`
- Email with attachments:
  `/home/joshazimullah.linux/work_mounts/patroni_rewrite/receive_mail/reply_with_attachement.sh`

Both scripts expect:

- Argument 1: recipient email address
- Argument 2: original subject line
- Body: either argument 3 or stdin
- Non-empty body is required

The scripts add `Re:` to the subject automatically if it is not already present.

## Plain email

```bash
/home/joshazimullah.linux/work_mounts/patroni_rewrite/receive_mail/reply.sh \
  "user@toffemail.nl" \
  "Status update" \
  "Hello,

The task is complete."
```

## Plain email from stdin

```bash
/home/joshazimullah.linux/work_mounts/patroni_rewrite/receive_mail/reply.sh \
  "user@toffemail.nl" \
  "Status update" <<'EOF'
Hello,

The task is complete.
EOF
```

## Pipe in a whole text file

```bash
cat /abs/path/message.txt | /home/joshazimullah.linux/work_mounts/patroni_rewrite/receive_mail/reply.sh \
  "user@toffemail.nl" \
  "Status update"
```

You can also redirect the file into stdin:

```bash
/home/joshazimullah.linux/work_mounts/patroni_rewrite/receive_mail/reply.sh \
  "user@toffemail.nl" \
  "Status update" < /abs/path/message.txt
```

## Email with attachments

Use one or more `--attach` flags:

```bash
/home/joshazimullah.linux/work_mounts/patroni_rewrite/receive_mail/reply_with_attachement.sh \
  "user@toffemail.nl" \
  "Status update" \
  --attach /abs/path/report.pdf \
  --attach /abs/path/log.txt \
  "Please see the attached files."
```

## Email with attachments and body from a whole text file

```bash
cat /abs/path/message.txt | /home/joshazimullah.linux/work_mounts/patroni_rewrite/receive_mail/reply_with_attachement.sh \
  "user@toffemail.nl" \
  "Status update" \
  --attach /abs/path/report.pdf
```
