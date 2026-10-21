// SPDX-License-Identifier: AGPL-3.0-only

package authn

import "strings"

// commonPasswords holds frequent breached passwords of 10+ characters (shorter
// ones are already rejected by the length rule), compared case-insensitively.
// Kept small on purpose (~2 KB); trivial patterns are caught by isTrivial.
var commonPasswords = func() map[string]struct{} {
	list := `1234567890 0123456789 0987654321 9876543210 1111111111 0000000000 1234512345 1122334455
1q2w3e4r5t 1q2w3e4r5t6y q1w2e3r4t5 q1w2e3r4t5y6 1qaz2wsx3edc qazwsxedcrfv zaq12wsxcde3 1qazxsw23edc
qwertyuiop qwertyuiop1 qwerty1234 qwerty12345 qwerty123456 asdfghjkl1 asdfghjkl; zxcvbnm123 zxcvbnmasd
password12 password123 password1234 password12345 password1! password01 passw0rd12 p@ssw0rd123 p@ssword123
passwordpassword password123! mypassword mypassword1 newpassword newpassword1 changeme123 changeme1!
iloveyou12 iloveyou123 iloveyou1! ilovemymom iloveyou2 iloveu1234 football12 football123 baseball12
basketball basketball1 soccer1234 superman12 superman123 batman1234 starwars12 starwars123 pokemon123
princess12 princess123 sunshine12 sunshine123 welcome123 welcome1234 welcome1! letmein123 letmein1234
monkey1234 dragon1234 shadow1234 master1234 michael123 jennifer12 jordan2323 abc1234567 abcdefghij
abcdefg123 abcd123456 abcdef1234 a1b2c3d4e5 aa12345678 aaaaaaaaaa computer12 computer123 internet12
administrator admin12345 admin123456 adminadmin root123456 test123456 testtest12 qwer123456 1234qwerty
123456789a 123456789q a123456789 q123456789 1234567890a 12345678910 123123123123 123456123456 147258369
1472583690 1597534560 7894561230 9638527410 0147258369 1357924680 2468135790 1234554321 5555555555
6666666666 7777777777 8888888888 9999999999 2222222222 3333333333 4444444444 12qwaszx12 trustno1234
liverpool1 chelsea123 arsenal123 manchester juventus12 barcelona1 realmadrid ferrari123 mercedes12
michelle12 jessica123 charlie123 jonathan12 elizabeth1 christopher alexander1 victoria12 samantha12
whatever12 freedom123 hello12345 helloworld helloworld1 iloveyou!! sunflower1 chocolate1 butterfly1
blink18218 fuckyou123 qwerty1! zxcvbnm1234 1qaz@wsx3edc simpletickets simpletickets1 helpdesk123 support123`
	m := map[string]struct{}{}
	for _, w := range strings.Fields(list) {
		m[strings.ToLower(w)] = struct{}{}
	}
	return m
}()

func isCommonPassword(pw string) bool {
	l := strings.ToLower(pw)
	if _, ok := commonPasswords[l]; ok {
		return true
	}
	return isTrivial(l)
}

// isTrivial catches a single repeated character and straight runs such as
// "abcdefghijk" or "9876543210…".
func isTrivial(s string) bool {
	if len(s) == 0 {
		return true
	}
	same, up, down := true, true, true
	for i := 1; i < len(s); i++ {
		d := int(s[i]) - int(s[i-1])
		same = same && d == 0
		up = up && d == 1
		down = down && d == -1
	}
	return same || up || down
}
