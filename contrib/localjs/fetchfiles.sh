#!/bin/bash

# List of scripts to fetch and store locally
whattofetch=(
	"https://cdnjs.cloudflare.com/ajax/libs/flot/0.8.3/excanvas.js"
	"https://cdnjs.cloudflare.com/ajax/libs/flot/0.8.3/excanvas.min.js"
	"https://cdnjs.cloudflare.com/ajax/libs/jquery/3.3.1/jquery.js"
	"https://cdnjs.cloudflare.com/ajax/libs/jquery/3.3.1/jquery.min.js"
	"https://cdnjs.cloudflare.com/ajax/libs/flot/0.8.3/jquery.flot.js"
	"https://cdnjs.cloudflare.com/ajax/libs/flot/0.8.3/jquery.flot.min.js"
	"https://cdnjs.cloudflare.com/ajax/libs/flot/0.8.3/jquery.flot.pie.js"
	"https://cdnjs.cloudflare.com/ajax/libs/flot/0.8.3/jquery.flot.pie.min.js"
	"https://cdnjs.cloudflare.com/ajax/libs/flot.tooltip/0.9.0/jquery.flot.tooltip.js"
	"https://cdnjs.cloudflare.com/ajax/libs/flot.tooltip/0.9.0/jquery.flot.tooltip.min.js"
	"https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.0.2/leaflet.css"
	"https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.0.2/leaflet.js"
	"https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.0.2/images/marker-icon.png"
	"https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.0.2/images/marker-shadow.png"
	"https://cdnjs.cloudflare.com/ajax/libs/leaflet.markercluster/1.0.0/MarkerCluster.css"
)

showhelp()
{
	echo "Syntax: $0 directory"
	echo "where directory is the directory in which you want to store the downloaded files."
	echo ""
}

getlocalfilename ()
{
	local sfn="$1"
	lfn=${sfn#https://cdnjs.cloudflare.com/ajax/libs}
}

downloadfile ()
{
	curl=`which curl`
	if [ ${#curl} -ge 4 ] ; then
		$curl --output "$2" "$1"
		return
	fi
	wget=`which wget`
	if [ ${#wget} -ge 4 ] ; then
		$wget --output-document="$2" "$1"
		return
	fi
	echo "ERROR: Neither curl nor wget were found in path. Please install either curl or wget."
	exit 1
}

if [ "$#" -ne 1 ] ; then
	showhelp
	exit 1
fi
if [ "$1" == "--help" -o "$1" == "-h" ] ; then
	showhelp
	exit 0
fi
localdir="$1"
if [ ! -d "$localdir" ] ; then
	echo "Target directory ${localdir} does not exist or is not a directory."
	showhelp
	exit 1
fi

unzip=`which unzip`
if [ ${#unzip} -lt 5 ] ; then
	echo "ERROR: unzip was not found in path. Please install unzip."
	exit 1
fi
for sf in ${whattofetch[@]}; do
	lfn="/void/void/void/void/"
	getlocalfilename "$sf"
	# lfn is now filled.
	tf="${localdir}${lfn}"
	if [ -e "$tf" ] ; then
		echo "No need to fetch $sf to $tf, it already exists."
	else
		tdn=`dirname "${tf}"`
		if [ ! -e "$tdn" ] ; then
			mkdir -p "$tdn"
		fi
		downloadfile "$sf" "$tf"
	fi
done

# The font stuff is a bit more messy.

# For the Lato font, we rely on a service that's packing the mess into
# a ZIP file for us.
if [ ! -e "${localdir}/fonts" ] ; then
	mkdir -p "${localdir}/fonts"
fi
downloadfile "https://google-webfonts-helper.herokuapp.com/api/fonts/lato?download=zip&subsets=latin&variants=900,regular" "${localdir}/fonts/lato-font-900and400.zip"
# Make sure the webserver cannot access the file for licensing reasons, as
# that would count as distribution and require proper attribution which
# we cannot guarantee.
chmod go-rwx "${localdir}/fonts/lato-font-900and400.zip"
unzip "${localdir}/fonts/lato-font-900and400.zip" -d "${localdir}/fonts/"

# For fontawesome, we download and unpack the whole ZIP as well.
# Downloading individual files will not work here, as depending on the
# browser random other files will be included.
if [ ! -e "${localdir}/font-awesome" ] ; then
	mkdir -p "${localdir}/font-awesome"
fi
if [ -e "${localdir}/font-awesome/4.7.0" ] ; then
	# Only download and extract if that directory isn't there yet, as we
	# have to use a 'mv' command here that would fail if the target already
	# existed.
	echo "Note: Skipping font-awesome download and extraction, it seems to be in place already."
	echo "To force redownload and extraction, remove '${localdir}/font-awesome/4.7.0'"
else
	downloadfile "https://fontawesome.com/v4.7.0/assets/font-awesome-4.7.0.zip" "${localdir}/font-awesome-4.7.0.zip"
	chmod go-rwx "${localdir}/font-awesome-4.7.0.zip"
	unzip "${localdir}/font-awesome-4.7.0.zip" -d "${localdir}/font-awesome"
	mv "${localdir}/font-awesome/font-awesome-4.7.0" "${localdir}/font-awesome/4.7.0"
fi

