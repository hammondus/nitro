# Nitro Web Server
www.nitro.onl

## Purpose
Does the world need another Web Server.
Clearly the answer is: No....  There are plenty of good ones in existance.

I wanted a project to improve my Go programming skills, and in the process, create a web server for my own use that was:
- small
- performant
- zero config for internal usage
- cross platform: Mac, Linux an Windows
- a single statically compiled binary with no dependencies


### Small
A small, easy to audit code base with minimal dependencies. Use the Go standard library as much as possible

### Performant
Go has good performance, and with it's built in easy to use and efficient concurrency, I'm aiming for for a high performance web server

### Zero Config
If you want to host some files on an internal machine, you can simply run the binary and it will just start serving files in the directory it's located. 
Obviously zero config isn't suitable for a public webserver, but is very handy for some internal use cases.

For more complex requirements, a simple to use configuration file can be used.

### Cross Platform
Leveraging on the ease of GO's built in cross platform functionalily, the one code base can create binaries that have zero other dependencies to run.
This binaries will work on:
    - Mac OS
    - Most distributions of Linux. (AMD64, Arm64 & Arm32)
    - Windows 10 or later

#### Development and Testing
Nitro is developed and tested on Mac OS
Extensive testing and production use is on Linux
Only simple testing is done on Windows


### Release Notes

#### v0.0.1 - Initial Version
Works as a static html server

#### Bugs
If you go to a directly without index.html, it displays all the files in that directory
