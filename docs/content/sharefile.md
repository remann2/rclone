---
title: "Citrix ShareFile"
description: "Rclone docs for Citrix ShareFile"
versionIntroduced: "v1.50"
---

# {{< icon "fas fa-share-square" >}} Citrix ShareFile

[Citrix ShareFile](https://sharefile.com) is a secure file sharing and transfer service aimed as business.

## Configuration

The initial setup for Citrix ShareFile involves getting a token from
Citrix ShareFile which you can in your browser.  `rclone config` walks you
through it.

Here is an example of how to make a remote called `remote`.  First run:

     rclone config

This will guide you through an interactive setup process:

```
No remotes found, make a new one?
n) New remote
s) Set configuration password
q) Quit config
n/s/q> n
name> remote
Type of storage to configure.
Enter a string value. Press Enter for the default ("").
Choose a number from below, or type in your own value
XX / Citrix Sharefile
   \ "sharefile"
Storage> sharefile
** See help for sharefile backend at: https://rclone.org/sharefile/ **

ID of the root folder

Leave blank to access "Personal Folders".  You can use one of the
standard values here or any folder ID (long hex number ID).
Enter a string value. Press Enter for the default ("").
Choose a number from below, or type in your own value
 1 / Access the Personal Folders. (Default)
   \ ""
 2 / Access the Favorites folder.
   \ "favorites"
 3 / Access all the shared folders.
   \ "allshared"
 4 / Access all the individual connectors.
   \ "connectors"
 5 / Access the home, favorites, and shared folders as well as the connectors.
   \ "top"
root_folder_id> 
Edit advanced config? (y/n)
y) Yes
n) No
y/n> n
Remote config
Use web browser to automatically authenticate rclone with remote?
 * Say Y if the machine running rclone has a web browser you can use
 * Say N if running rclone on a (remote) machine without web browser access
If not sure try Y. If Y failed, try N.
y) Yes
n) No
y/n> y
If your browser doesn't open automatically go to the following link: http://127.0.0.1:53682/auth?state=XXX
Log in and authorize rclone for access
Waiting for code...
Got code
Configuration complete.
Options:
- type: sharefile
- endpoint: https://XXX.sharefile.com
- token: {"access_token":"XXX","token_type":"bearer","refresh_token":"XXX","expiry":"2019-09-30T19:41:45.878561877+01:00"}
Keep this "remote" remote?
y) Yes this is OK
e) Edit this remote
d) Delete this remote
y/e/d> y
```

See the [remote setup docs](/remote_setup/) for how to set it up on a
machine with no Internet browser available.

Note that rclone runs a webserver on your local machine to collect the
token as returned from Citrix ShareFile. This only runs from the moment it opens
your browser to the moment you get back the verification code.  This
is on `http://127.0.0.1:53682/` and this it may require you to unblock
it temporarily if you are running a host firewall.

Once configured you can then use `rclone` like this,

List directories in top level of your ShareFile

    rclone lsd remote:

List all the files in your ShareFile

    rclone ls remote:

To copy a local directory to an ShareFile directory called backup

    rclone copy /home/source remote:backup

Paths may be as deep as required, e.g. `remote:directory/subdirectory`.

### Modification times and hashes

ShareFile allows modification times to be set on objects accurate to 1
second.  These will be used to detect whether objects need syncing or
not.

ShareFile supports MD5 type hashes, so you can use the `--checksum`
flag.

### Transfers

For files above 128 MiB rclone will use a chunked transfer.  Rclone will
upload up to `--transfers` chunks at the same time (shared among all
the multipart uploads).  Chunks are buffered in memory and are
normally 64 MiB so increasing `--transfers` will increase memory use.

### Restricted filename characters

In addition to the [default restricted characters set](/overview/#restricted-characters)
the following characters are also replaced:

| Character | Value | Replacement |
| --------- |:-----:|:-----------:|
| \\        | 0x5C  | ＼           |
| *         | 0x2A  | ＊           |
| <         | 0x3C  | ＜           |
| >         | 0x3E  | ＞           |
| ?         | 0x3F  | ？           |
| :         | 0x3A  | ：           |
| \|        | 0x7C  | ｜           |
| "         | 0x22  | ＂           |

File names can also not start or end with the following characters.
These only get replaced if they are the first or last character in the
name:

| Character | Value | Replacement |
| --------- |:-----:|:-----------:|
| SP        | 0x20  | ␠           |
| .         | 0x2E  | ．           |

Invalid UTF-8 bytes will also be [replaced](/overview/#invalid-utf8),
as they can't be used in JSON strings.

{{< rem autogenerated options start" - DO NOT EDIT - instead edit fs.RegInfo in backend/sharefile/sharefile.go then run make backenddocs" >}}
### Standard options

Here are the Standard options specific to sharefile (Citrix Sharefile).

#### --sharefile-client-id

OAuth Client Id.

Leave blank normally.

Properties:

- Config:      client_id
- Env Var:     RCLONE_SHAREFILE_CLIENT_ID
- Type:        string
- Required:    false

#### --sharefile-client-secret

OAuth Client Secret.

Leave blank normally.

Properties:

- Config:      client_secret
- Env Var:     RCLONE_SHAREFILE_CLIENT_SECRET
- Type:        string
- Required:    false

#### --sharefile-root-folder-id

ID of the root folder.

Leave blank to access "Personal Folders".  You can use one of the
standard values here or any folder ID (long hex number ID).

Properties:

- Config:      root_folder_id
- Env Var:     RCLONE_SHAREFILE_ROOT_FOLDER_ID
- Type:        string
- Required:    false
- Examples:
    - ""
        - Access the Personal Folders (default).
    - "favorites"
        - Access the Favorites folder.
    - "allshared"
        - Access all the shared folders.
    - "connectors"
        - Access all the individual connectors.
    - "top"
        - Access the home, favorites, and shared folders as well as the connectors.

### Advanced options

Here are the Advanced options specific to sharefile (Citrix Sharefile).

#### --sharefile-token

OAuth Access Token as a JSON blob.

Properties:

- Config:      token
- Env Var:     RCLONE_SHAREFILE_TOKEN
- Type:        string
- Required:    false

#### --sharefile-auth-url

Auth server URL.

Leave blank to use the provider defaults.

Properties:

- Config:      auth_url
- Env Var:     RCLONE_SHAREFILE_AUTH_URL
- Type:        string
- Required:    false

#### --sharefile-token-url

Token server url.

Leave blank to use the provider defaults.

Properties:

- Config:      token_url
- Env Var:     RCLONE_SHAREFILE_TOKEN_URL
- Type:        string
- Required:    false

#### --sharefile-client-credentials

Use client credentials OAuth flow.

This will use the OAUTH2 client Credentials Flow as described in RFC 6749.

Properties:

- Config:      client_credentials
- Env Var:     RCLONE_SHAREFILE_CLIENT_CREDENTIALS
- Type:        bool
- Default:     false

#### --sharefile-upload-cutoff

Cutoff for switching to multipart upload.

Properties:

- Config:      upload_cutoff
- Env Var:     RCLONE_SHAREFILE_UPLOAD_CUTOFF
- Type:        SizeSuffix
- Default:     128Mi

#### --sharefile-chunk-size

Upload chunk size.

Must a power of 2 >= 256k.

Making this larger will improve performance, but note that each chunk
is buffered in memory one per transfer.

Reducing this will reduce memory usage but decrease performance.

Properties:

- Config:      chunk_size
- Env Var:     RCLONE_SHAREFILE_CHUNK_SIZE
- Type:        SizeSuffix
- Default:     64Mi

#### --sharefile-endpoint

Endpoint for API calls.

This is usually auto discovered as part of the oauth process, but can
be set manually to something like: https://XXX.sharefile.com


Properties:

- Config:      endpoint
- Env Var:     RCLONE_SHAREFILE_ENDPOINT
- Type:        string
- Required:    false

#### --sharefile-encoding

The encoding for the backend.

See the [encoding section in the overview](/overview/#encoding) for more info.

Properties:

- Config:      encoding
- Env Var:     RCLONE_SHAREFILE_ENCODING
- Type:        Encoding
- Default:     Slash,LtGt,DoubleQuote,Colon,Question,Asterisk,Pipe,BackSlash,Ctl,LeftSpace,LeftPeriod,RightSpace,RightPeriod,InvalidUtf8,Dot

#### --sharefile-description

Description of the remote.

Properties:

- Config:      description
- Env Var:     RCLONE_SHAREFILE_DESCRIPTION
- Type:        string
- Required:    false

{{< rem autogenerated options stop >}}
## Limitations

Note that ShareFile is case insensitive so you can't have a file called
"Hello.doc" and one called "hello.doc".

ShareFile only supports filenames up to 256 characters in length.

`rclone about` is not supported by the Citrix ShareFile backend. Backends without
this capability cannot determine free space for an rclone mount or
use policy `mfs` (most free space) as a member of an rclone union
remote.

See [List of backends that do not support rclone about](https://rclone.org/overview/#optional-features) and [rclone about](https://rclone.org/commands/rclone_about/)

