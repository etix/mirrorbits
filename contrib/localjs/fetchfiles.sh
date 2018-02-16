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
	if [ ${#curl} -gt 4 ] ; then
		$curl --output "$2" "$1"
		return
	fi
	wget=`which wget`
	if [ ${#wget} -gt 4 ] ; then
		$wget --output-document="$2" "$1"
		return
	fi
	echo "Sorry: Neither curl nor wget were found in path."
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

