	var channelStringId = "";
	var channelNumericalId = "0";
	var tracksHistory = null;
    var _music_info_template_str = $("#music_info_template").html();
    var _music_info_template = Handlebars.compile(_music_info_template_str);
    var _history_template_str = $("#history_template").html();
    var _history_template = Handlebars.compile(_history_template_str);
	var song = null;
    function show_song(song) {
		song.thumb = song.song_link + "?thumb";
		if(song.song_link.includes("youtu.be/")){
			song.thumb = "https://i3.ytimg.com/vi/"+song.song_link.replace("https://youtu.be/", "")+"/hqdefault.jpg";
		}
		//var music_info_html = _music_info_template(song);
		var music_info_html = Mustache.render(_music_info_template_str, song);
		$('#music_info').html(music_info_html);
		if(window.Twitch.configuration != undefined) {
			if(song.artist.length > 17) {
				$('#track-artist').css('font-size', '18px');
			}
			if(song.title.length > 13) {
				$('#track-title').css('font-size', '22px');
				$('#track-artist').css('font-size', '18px');
			}
			if(song.artist.length > 21 && song.title.length > 17) {
				$('#track-title').css('font-size', '22px');
			}
			if(song.artist.length > 25) {
				$('#track-artist').css('font-size', '16px');
			}
			if(song.artist.length > 35) {
				$('#track-artist').css('font-size', '11px');
			}
			if(song.title.length > 25) {
				$('#track-title').css('font-size', '18px');
				$('#track-artist').css('font-size', '16px');
			}
			if(song.title.length > 35) {
				$('#track-title').css('font-size', '13px');
				$('#track-artist').css('font-size', '11px');
			}
		}
		var tempHistory = Object();
		tempHistory.Elements = Array();
		for(i = tracksHistory.Elements.length-1; i >= 0; i--) {
			var element = tracksHistory.Elements[(i+tracksHistory.OldestElement)%tracksHistory.Elements.length]
			if(element == null) continue;
			var date = new Date(element.song_length+" +0300");
			element.time = date.getHours().pad(2) + ":" + date.getMinutes().pad(2);
			tempHistory.Elements.push(element)
		}
		//var history_html = _history_template(tempHistory);
		var history_html = Mustache.render(_history_template_str, tempHistory);
		$('#history').html(history_html);		
    }

	function update_history(channelId) {
		$.ajax({
		  url: "https://api.audd.io/lastSong/getChannelById/?ch_id="+channelId,
		  type: 'GET',
		  success: function(data) {
			console.log(data);
			channelNumericalId = data.NumericalId;
			channelStringId = data.StringId;
			tracksHistory = data.History;
			if(song == null) {
				song = tracksHistory.Elements[(tracksHistory.OldestElement-1)%tracksHistory.Elements.length];
				show_song(song);
			} else {
				tracksHistory.Elements[tracksHistory.OldestElement] = song;
				tracksHistory.OldestElement = (tracksHistory.OldestElement + 1) % tracksHistory.Elements.length;
			}
		  }
		});
	}
	function new_song(song, channelId) {
		show_song(song);
		if(tracksHistory == null) {
			update_history(channelId);
		} else {
			tracksHistory.Elements[tracksHistory.OldestElement] = song;
			tracksHistory.OldestElement = (tracksHistory.OldestElement + 1) % tracksHistory.Elements.length;
		}
	}
	function callback(target, contentType, message){
			song = JSON.parse(message);
			new_song(song, auth.channelId)
	}
	if(window.Twitch.configuration != undefined) {
		window.Twitch.ext.onAuthorized(function(auth) {
		  console.log(auth);
		  console.log('The JWT that will be passed to the EBS is', auth.token);
		  console.log('The channel ID is', auth.channelId);
		  window.Twitch.ext.unlisten("broadcast", callback);
		  window.Twitch.ext.listen("broadcast", callback);
		  
			update_history(auth.channelId);
		});
	} else {
		var sinceTime = (new Date(Date.now())).getTime();
		function poll() {
			var timeout = 30;
			var optionalSince = "";
			if (sinceTime) {
				optionalSince = "&since_time=" + sinceTime;
			}
			var pollUrl = "https://api.audd.io/lastSong/longpoll/?timeout=" + timeout + "&category=" + channelId + optionalSince;
			var successDelay = 10;  // 10 ms
			var errorDelay = 3000;  // 3 sec
			$.ajax({ url: pollUrl,
				success: function(data) {
					if (data && data.events && data.events.length > 0) {
						for (var i = 0; i < data.events.length; i++) {
							var event = data.events[i];
							song = event.data;
							new_song(song, channelId)
							sinceTime = event.timestamp;
						}
						setTimeout(poll, successDelay);
						return;
					}
					if (data && data.timeout) {
						setTimeout(poll, successDelay);
						return;
					}
					if (data && data.error) {
						setTimeout(poll, errorDelay);
						return;
					}
					// We should have gotten one of the above 3 cases:
					// either nonempty event data, a timeout, or an error.
					console.log("Didn't get expected event data, try again shortly...");
					setTimeout(poll, errorDelay);
				},
				dataType: "json",
				error: function (data) {
					console.log("Error in ajax request--trying again shortly...");
					setTimeout(poll, errorDelay);
				}
			});
		};
		var channelId = new URLSearchParams(window.location.search).get("ch");
		poll();
		update_history(channelId);
		
	}
	
	Number.prototype.pad = function(size) {
		var s = String(this);
		while (s.length < (size || 2)) {s = "0" + s;}
		return s;
	}