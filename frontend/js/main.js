	var channelStringId = "";
	var channelNumericalId = "0";
	var tracksHistory = null;
    var _music_info_template_str = $("#music_info_template").html();
    //var _music_info_template = Handlebars.compile(_music_info_template_str);
    var _history_template_str = $("#history_template").html();
    //var _history_template = Handlebars.compile(_history_template_str);
	var song = null;
	var token = "";
	var channelId;
	var is_mobile = "mobile" == new URLSearchParams(window.location.search).get("platform");
    function show_song(song) {
		song.thumb = song.song_link + "?thumb";
		if(song.song_link.includes("youtu.be/")){
			song.thumb = "https://i3.ytimg.com/vi/"+song.song_link.replace("https://youtu.be/", "")+"/hqdefault.jpg";
		}
		//var music_info_html = _music_info_template(song);
		var music_info_html = Mustache.render(_music_info_template_str, song);
		$('#music_info').html(music_info_html);
		if(window.Twitch.ext.configuration != undefined) {
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
			if(song.title.length > 35 || is_mobile) {
				$('#track-title').css('font-size', '13px');
				$('#track-artist').css('font-size', '11px');
			}
		}
		var tempHistory = Object();
		tempHistory.Elements = Array();
		for(i = tracksHistory.Elements.length-1; i >= 0; i--) {
			var element = tracksHistory.Elements[(i+tracksHistory.OldestElement)%tracksHistory.Elements.length]
			if(element == null) continue;
			var date = dateFromString(element.song_length);
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
			new_song(song, channelId)
	}
	if(window.Twitch.ext.configuration != undefined) {
		window.Twitch.ext.onAuthorized(function(auth) {
		  console.log(auth);
		  console.log('The JWT that will be passed to the EBS is', auth.token);
		  token = auth.token;
		  console.log('The channel ID is', auth.channelId);
		  channelId = auth.channelId;
		  window.Twitch.ext.unlisten("broadcast", callback);
		  window.Twitch.ext.listen("broadcast", callback);
		  
			update_history(auth.channelId);
			
			if(document.getElementById("config-body") != null) {
				configPage();
			}
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
	
	// The code below is executed only on the config page
	// The server verifies the JWT and checks stuff like if the user is a broadcaster
		
	var configPageCalled = false;
	function configPage() {
		if(configPageCalled) return;
		configPageCalled = true;
		document.getElementById("haveToken-button").addEventListener("click", haveToken);
		document.getElementById("signUp-button").addEventListener("click", signUp);
		document.getElementById("back-button").addEventListener("click", back);
		document.getElementById("saveToken-button").addEventListener("click", saveToken);
		document.getElementById("copy-button").addEventListener("click", copyObsLink);
		document.getElementById("copy-button2").addEventListener("click", copyWidgetLink);
		$.ajax({
		  url: "https://dashboard.audd.io/api/twitch.php?get_user",
		  type: 'POST',
		  data: {jwt: token},
		  success: function(data) {
			if(data[0]) {
				configSuccess(data);
				return;
			}
			$('#account-question').show();
			if(data[1] != "no_user") console.log("A possible error with the JWT token");
		  },
		  dataType: "json",
		});
	}
	
	function haveToken() {
		$('#account-question').hide();
		$('#enter-token').show();
	}
	function back() {
		$('#enter-token').hide();
		$('#account-question').show();
	}
	
	function signUp() {
		$('#account-question').hide();
		$.ajax({
		  url: "https://dashboard.audd.io/api/twitch.php?signup",
		  type: 'POST',
		  data: {jwt: token},
		  success: function(data) {
			if(data[0]) {
				$('#account-question').hide();
				configSuccess(data);
				return;
			}
			tokenSaveError(data);
		  },
		  error: function(data) {
			// Don't send anything exteral to DOM
			$("#error").text("Sorry, there was an error when we tried to send a request to our backend. Please try again later or contact our support: hello@audd.io."); 
			$("#error").show();
		  },
		  dataType: "json",
		});
	}
	function saveToken() {
		$('#enter-token').hide();
		var apiToken = $('#api-token').val();
		$.ajax({
		  url: "https://dashboard.audd.io/api/twitch.php?save_user",
		  type: 'POST',
		  data: {jwt: token, api_token: apiToken},
		  success: function(data) {
			if(data[0]) {
				configSuccess(data);
				return;
			}
			tokenSaveError(data);
		  },
		  dataType: "json",
		});
	}
	
	function getRenewalLink(userData) {		
		var user_id = encodeURIComponent(userData.user_id);
		var h_sign = encodeURIComponent(userData.payments_h);
		
		var payment_link = "https://payments.audd.io/?user_id="+user_id+"&h="+h_sign+"&action=make_streams_payment-1";
		
		// all the parameters are urlencoded and the link starts with https://
		return payment_link;
	}
	var obsLink = "";
	var widgetLink = "";
	function configSuccess(apiData) {
		$("#subscription-info-text").text("📅 Music recognition for the stream is active till");
		$("#date-till").text(apiData[1].streams_till_text);
		if(apiData[1].suggest_renewal) {
			var payment_link = getRenewalLink(apiData[1]);
			$("#renew-button").attr("href", payment_link);
			$("#renew-button").show();
		}
		$("#subscription-info").show();
		var uid = encodeURIComponent(apiData[1].twitch_id);
		var obs_sign = encodeURIComponent(apiData[1].obs_sign);
		obsLink = "https://api.audd.io/lastSong/obs/?uid="+uid+"&s="+obs_sign;
		$("#obs-link-input").val(obsLink);
		$("#obs-link").show();
		widgetLink = "https://audd.tech/twitch/?ch="+uid;
		$("#widget-link-input").val(widgetLink);
		$("#widget-link").show();
		$("#preview").show();
	}
	function copyTextToClipboard(text) {
	  var textArea = document.createElement("textarea");
	  textArea.value = text;
	  
	  // Avoid scrolling to bottom
	  textArea.style.top = "0";
	  textArea.style.left = "0";
	  textArea.style.position = "fixed";

	  document.body.appendChild(textArea);
	  textArea.focus();
	  textArea.select();

	  try {
		var successful = document.execCommand('copy');
		var msg = successful ? 'successful' : 'unsuccessful';
		console.log('Fallback: Copying text command was ' + msg);
	  } catch (err) {
		console.error('Fallback: Oops, unable to copy', err);
	  }

	  document.body.removeChild(textArea);
	}
	function copyObsLink() {
	  copyTextToClipboard(obsLink);
	  $('#copy-button').text('Copied');
	  setTimeout(() => { 
		 $('#copy-button').text('Copy');
	  }, 2000);
	}
	function copyWidgetLink() {
	  copyTextToClipboard(widgetLink);
	  $('#copy-button2').text('Copied');
	  setTimeout(() => { 
		 $('#copy-button2').text('Copy');
	  }, 2000);
	}
	function tokenSaveError(apiData) {
		// Don't send anything exteral to DOM
		switch(apiData[1]) {
			case "internal_error":
				$("#error").text("Sorry, there was an internal error on our server. Please try again later or contact our support: hello@audd.io."); 
				$("#error").show();
				break;
			case "no_jwt":
				$("#error").text("Sorry, there was an error: we couldn't authorize the Twitch JWT on our backend. Please try again or contact our support: hello@audd.io"); 
				$("#error").show();
				break;
			case "wrong_jwt":
				$("#error").text("Sorry, there was an error: we couldn't authorize the Twitch JWT on our backend. Please try again or contact our support: hello@audd.io"); 
				$("#error").show();
				break;
			case "no_token":
				$("#error").text("Sorry, the api_token can't be empty."); 
				$("#error").show();
				break;
			case "wrong_token":
				$("#error").text("Sorry, we can't find a user with such api_token. If you think that's a mistake, please contact the API support: api@audd.io."); 
				$("#error").show();
				break;
			case "zero_limit":
				if(apiData[2].streams_till_text != undefined) {
					$("#subscription-info-text").text("📅 Music recognition for the stream was active till");
					$("#date-till").text(apiData[2].streams_till_text); // fixed a bug 
				} else {
					$("#subscription-info-text").text("Activate music recognition for streams for a month for $45. If you want to test our extension for free, let us know: hello@audd.io");
					$("#renew-button").text("Activate for $45");
				}
				var payment_link = getRenewalLink(apiData[2]);
				$("#renew-button").attr("href", payment_link);
				$("#renew-button").show();
				$("#subscription-info").show();
				break;
		}
	}
	
	// For Safari compatibility, added this function to use instead of the default Date parsing. Sources: https://stackoverflow.com/a/9413229, https://stackoverflow.com/a/1050782
	function dateFromString(str) { 
	  var a = $.map(str.split(/[^0-9]/), function(s) { return parseInt(s, 10) });
	  return new Date(Date.UTC(a[0], a[1]-1 || 0, a[2] || 1, a[3] || 0, a[4] || 0, a[5] || 0, a[6] || 0)).addHours(-3); // the callbacks from AudD API are at UTC+3
	}
	Date.prototype.addHours = function(h) {
	  this.setTime(this.getTime() + (h*60*60*1000));
	  return this;
	}