// Package big holds one function too large for a single chunk.
package big

import "fmt"

// Process runs every step in order and reports each result.
// The body is long enough to be split into parts that repeat the signature.
func Process(steps []string, verbose bool) (int, error) {
	total := 0
	// step 1
	if len(steps) > 1 && steps[1] != "" {
		total += len(steps[1]) * 1
		if verbose {
			fmt.Printf("step %d: %s\n", 1, steps[1])
		}
	}
	// step 2
	if len(steps) > 2 && steps[2] != "" {
		total += len(steps[2]) * 2
		if verbose {
			fmt.Printf("step %d: %s\n", 2, steps[2])
		}
	}
	// step 3
	if len(steps) > 3 && steps[3] != "" {
		total += len(steps[3]) * 3
		if verbose {
			fmt.Printf("step %d: %s\n", 3, steps[3])
		}
	}
	// step 4
	if len(steps) > 4 && steps[4] != "" {
		total += len(steps[4]) * 4
		if verbose {
			fmt.Printf("step %d: %s\n", 4, steps[4])
		}
	}
	// step 5
	if len(steps) > 5 && steps[5] != "" {
		total += len(steps[5]) * 5
		if verbose {
			fmt.Printf("step %d: %s\n", 5, steps[5])
		}
	}
	// step 6
	if len(steps) > 6 && steps[6] != "" {
		total += len(steps[6]) * 6
		if verbose {
			fmt.Printf("step %d: %s\n", 6, steps[6])
		}
	}
	// step 7
	if len(steps) > 7 && steps[7] != "" {
		total += len(steps[7]) * 7
		if verbose {
			fmt.Printf("step %d: %s\n", 7, steps[7])
		}
	}
	// step 8
	if len(steps) > 8 && steps[8] != "" {
		total += len(steps[8]) * 8
		if verbose {
			fmt.Printf("step %d: %s\n", 8, steps[8])
		}
	}
	// step 9
	if len(steps) > 9 && steps[9] != "" {
		total += len(steps[9]) * 9
		if verbose {
			fmt.Printf("step %d: %s\n", 9, steps[9])
		}
	}
	// step 10
	if len(steps) > 10 && steps[10] != "" {
		total += len(steps[10]) * 10
		if verbose {
			fmt.Printf("step %d: %s\n", 10, steps[10])
		}
	}
	// step 11
	if len(steps) > 11 && steps[11] != "" {
		total += len(steps[11]) * 11
		if verbose {
			fmt.Printf("step %d: %s\n", 11, steps[11])
		}
	}
	// step 12
	if len(steps) > 12 && steps[12] != "" {
		total += len(steps[12]) * 12
		if verbose {
			fmt.Printf("step %d: %s\n", 12, steps[12])
		}
	}
	// step 13
	if len(steps) > 13 && steps[13] != "" {
		total += len(steps[13]) * 13
		if verbose {
			fmt.Printf("step %d: %s\n", 13, steps[13])
		}
	}
	// step 14
	if len(steps) > 14 && steps[14] != "" {
		total += len(steps[14]) * 14
		if verbose {
			fmt.Printf("step %d: %s\n", 14, steps[14])
		}
	}
	// step 15
	if len(steps) > 15 && steps[15] != "" {
		total += len(steps[15]) * 15
		if verbose {
			fmt.Printf("step %d: %s\n", 15, steps[15])
		}
	}
	// step 16
	if len(steps) > 16 && steps[16] != "" {
		total += len(steps[16]) * 16
		if verbose {
			fmt.Printf("step %d: %s\n", 16, steps[16])
		}
	}
	// step 17
	if len(steps) > 17 && steps[17] != "" {
		total += len(steps[17]) * 17
		if verbose {
			fmt.Printf("step %d: %s\n", 17, steps[17])
		}
	}
	// step 18
	if len(steps) > 18 && steps[18] != "" {
		total += len(steps[18]) * 18
		if verbose {
			fmt.Printf("step %d: %s\n", 18, steps[18])
		}
	}
	// step 19
	if len(steps) > 19 && steps[19] != "" {
		total += len(steps[19]) * 19
		if verbose {
			fmt.Printf("step %d: %s\n", 19, steps[19])
		}
	}
	// step 20
	if len(steps) > 20 && steps[20] != "" {
		total += len(steps[20]) * 20
		if verbose {
			fmt.Printf("step %d: %s\n", 20, steps[20])
		}
	}
	// step 21
	if len(steps) > 21 && steps[21] != "" {
		total += len(steps[21]) * 21
		if verbose {
			fmt.Printf("step %d: %s\n", 21, steps[21])
		}
	}
	// step 22
	if len(steps) > 22 && steps[22] != "" {
		total += len(steps[22]) * 22
		if verbose {
			fmt.Printf("step %d: %s\n", 22, steps[22])
		}
	}
	// step 23
	if len(steps) > 23 && steps[23] != "" {
		total += len(steps[23]) * 23
		if verbose {
			fmt.Printf("step %d: %s\n", 23, steps[23])
		}
	}
	// step 24
	if len(steps) > 24 && steps[24] != "" {
		total += len(steps[24]) * 24
		if verbose {
			fmt.Printf("step %d: %s\n", 24, steps[24])
		}
	}
	// step 25
	if len(steps) > 25 && steps[25] != "" {
		total += len(steps[25]) * 25
		if verbose {
			fmt.Printf("step %d: %s\n", 25, steps[25])
		}
	}
	// step 26
	if len(steps) > 26 && steps[26] != "" {
		total += len(steps[26]) * 26
		if verbose {
			fmt.Printf("step %d: %s\n", 26, steps[26])
		}
	}
	// step 27
	if len(steps) > 27 && steps[27] != "" {
		total += len(steps[27]) * 27
		if verbose {
			fmt.Printf("step %d: %s\n", 27, steps[27])
		}
	}
	// step 28
	if len(steps) > 28 && steps[28] != "" {
		total += len(steps[28]) * 28
		if verbose {
			fmt.Printf("step %d: %s\n", 28, steps[28])
		}
	}
	// step 29
	if len(steps) > 29 && steps[29] != "" {
		total += len(steps[29]) * 29
		if verbose {
			fmt.Printf("step %d: %s\n", 29, steps[29])
		}
	}
	// step 30
	if len(steps) > 30 && steps[30] != "" {
		total += len(steps[30]) * 30
		if verbose {
			fmt.Printf("step %d: %s\n", 30, steps[30])
		}
	}
	// step 31
	if len(steps) > 31 && steps[31] != "" {
		total += len(steps[31]) * 31
		if verbose {
			fmt.Printf("step %d: %s\n", 31, steps[31])
		}
	}
	// step 32
	if len(steps) > 32 && steps[32] != "" {
		total += len(steps[32]) * 32
		if verbose {
			fmt.Printf("step %d: %s\n", 32, steps[32])
		}
	}
	// step 33
	if len(steps) > 33 && steps[33] != "" {
		total += len(steps[33]) * 33
		if verbose {
			fmt.Printf("step %d: %s\n", 33, steps[33])
		}
	}
	// step 34
	if len(steps) > 34 && steps[34] != "" {
		total += len(steps[34]) * 34
		if verbose {
			fmt.Printf("step %d: %s\n", 34, steps[34])
		}
	}
	// step 35
	if len(steps) > 35 && steps[35] != "" {
		total += len(steps[35]) * 35
		if verbose {
			fmt.Printf("step %d: %s\n", 35, steps[35])
		}
	}
	// step 36
	if len(steps) > 36 && steps[36] != "" {
		total += len(steps[36]) * 36
		if verbose {
			fmt.Printf("step %d: %s\n", 36, steps[36])
		}
	}
	// step 37
	if len(steps) > 37 && steps[37] != "" {
		total += len(steps[37]) * 37
		if verbose {
			fmt.Printf("step %d: %s\n", 37, steps[37])
		}
	}
	// step 38
	if len(steps) > 38 && steps[38] != "" {
		total += len(steps[38]) * 38
		if verbose {
			fmt.Printf("step %d: %s\n", 38, steps[38])
		}
	}
	// step 39
	if len(steps) > 39 && steps[39] != "" {
		total += len(steps[39]) * 39
		if verbose {
			fmt.Printf("step %d: %s\n", 39, steps[39])
		}
	}
	// step 40
	if len(steps) > 40 && steps[40] != "" {
		total += len(steps[40]) * 40
		if verbose {
			fmt.Printf("step %d: %s\n", 40, steps[40])
		}
	}
	// step 41
	if len(steps) > 41 && steps[41] != "" {
		total += len(steps[41]) * 41
		if verbose {
			fmt.Printf("step %d: %s\n", 41, steps[41])
		}
	}
	// step 42
	if len(steps) > 42 && steps[42] != "" {
		total += len(steps[42]) * 42
		if verbose {
			fmt.Printf("step %d: %s\n", 42, steps[42])
		}
	}
	// step 43
	if len(steps) > 43 && steps[43] != "" {
		total += len(steps[43]) * 43
		if verbose {
			fmt.Printf("step %d: %s\n", 43, steps[43])
		}
	}
	// step 44
	if len(steps) > 44 && steps[44] != "" {
		total += len(steps[44]) * 44
		if verbose {
			fmt.Printf("step %d: %s\n", 44, steps[44])
		}
	}
	// step 45
	if len(steps) > 45 && steps[45] != "" {
		total += len(steps[45]) * 45
		if verbose {
			fmt.Printf("step %d: %s\n", 45, steps[45])
		}
	}
	// step 46
	if len(steps) > 46 && steps[46] != "" {
		total += len(steps[46]) * 46
		if verbose {
			fmt.Printf("step %d: %s\n", 46, steps[46])
		}
	}
	// step 47
	if len(steps) > 47 && steps[47] != "" {
		total += len(steps[47]) * 47
		if verbose {
			fmt.Printf("step %d: %s\n", 47, steps[47])
		}
	}
	// step 48
	if len(steps) > 48 && steps[48] != "" {
		total += len(steps[48]) * 48
		if verbose {
			fmt.Printf("step %d: %s\n", 48, steps[48])
		}
	}
	// step 49
	if len(steps) > 49 && steps[49] != "" {
		total += len(steps[49]) * 49
		if verbose {
			fmt.Printf("step %d: %s\n", 49, steps[49])
		}
	}
	// step 50
	if len(steps) > 50 && steps[50] != "" {
		total += len(steps[50]) * 50
		if verbose {
			fmt.Printf("step %d: %s\n", 50, steps[50])
		}
	}
	// step 51
	if len(steps) > 51 && steps[51] != "" {
		total += len(steps[51]) * 51
		if verbose {
			fmt.Printf("step %d: %s\n", 51, steps[51])
		}
	}
	// step 52
	if len(steps) > 52 && steps[52] != "" {
		total += len(steps[52]) * 52
		if verbose {
			fmt.Printf("step %d: %s\n", 52, steps[52])
		}
	}
	// step 53
	if len(steps) > 53 && steps[53] != "" {
		total += len(steps[53]) * 53
		if verbose {
			fmt.Printf("step %d: %s\n", 53, steps[53])
		}
	}
	// step 54
	if len(steps) > 54 && steps[54] != "" {
		total += len(steps[54]) * 54
		if verbose {
			fmt.Printf("step %d: %s\n", 54, steps[54])
		}
	}
	// step 55
	if len(steps) > 55 && steps[55] != "" {
		total += len(steps[55]) * 55
		if verbose {
			fmt.Printf("step %d: %s\n", 55, steps[55])
		}
	}
	// step 56
	if len(steps) > 56 && steps[56] != "" {
		total += len(steps[56]) * 56
		if verbose {
			fmt.Printf("step %d: %s\n", 56, steps[56])
		}
	}
	// step 57
	if len(steps) > 57 && steps[57] != "" {
		total += len(steps[57]) * 57
		if verbose {
			fmt.Printf("step %d: %s\n", 57, steps[57])
		}
	}
	// step 58
	if len(steps) > 58 && steps[58] != "" {
		total += len(steps[58]) * 58
		if verbose {
			fmt.Printf("step %d: %s\n", 58, steps[58])
		}
	}
	// step 59
	if len(steps) > 59 && steps[59] != "" {
		total += len(steps[59]) * 59
		if verbose {
			fmt.Printf("step %d: %s\n", 59, steps[59])
		}
	}
	// step 60
	if len(steps) > 60 && steps[60] != "" {
		total += len(steps[60]) * 60
		if verbose {
			fmt.Printf("step %d: %s\n", 60, steps[60])
		}
	}
	// step 61
	if len(steps) > 61 && steps[61] != "" {
		total += len(steps[61]) * 61
		if verbose {
			fmt.Printf("step %d: %s\n", 61, steps[61])
		}
	}
	// step 62
	if len(steps) > 62 && steps[62] != "" {
		total += len(steps[62]) * 62
		if verbose {
			fmt.Printf("step %d: %s\n", 62, steps[62])
		}
	}
	// step 63
	if len(steps) > 63 && steps[63] != "" {
		total += len(steps[63]) * 63
		if verbose {
			fmt.Printf("step %d: %s\n", 63, steps[63])
		}
	}
	// step 64
	if len(steps) > 64 && steps[64] != "" {
		total += len(steps[64]) * 64
		if verbose {
			fmt.Printf("step %d: %s\n", 64, steps[64])
		}
	}
	// step 65
	if len(steps) > 65 && steps[65] != "" {
		total += len(steps[65]) * 65
		if verbose {
			fmt.Printf("step %d: %s\n", 65, steps[65])
		}
	}
	// step 66
	if len(steps) > 66 && steps[66] != "" {
		total += len(steps[66]) * 66
		if verbose {
			fmt.Printf("step %d: %s\n", 66, steps[66])
		}
	}
	// step 67
	if len(steps) > 67 && steps[67] != "" {
		total += len(steps[67]) * 67
		if verbose {
			fmt.Printf("step %d: %s\n", 67, steps[67])
		}
	}
	// step 68
	if len(steps) > 68 && steps[68] != "" {
		total += len(steps[68]) * 68
		if verbose {
			fmt.Printf("step %d: %s\n", 68, steps[68])
		}
	}
	// step 69
	if len(steps) > 69 && steps[69] != "" {
		total += len(steps[69]) * 69
		if verbose {
			fmt.Printf("step %d: %s\n", 69, steps[69])
		}
	}
	// step 70
	if len(steps) > 70 && steps[70] != "" {
		total += len(steps[70]) * 70
		if verbose {
			fmt.Printf("step %d: %s\n", 70, steps[70])
		}
	}
	// step 71
	if len(steps) > 71 && steps[71] != "" {
		total += len(steps[71]) * 71
		if verbose {
			fmt.Printf("step %d: %s\n", 71, steps[71])
		}
	}
	// step 72
	if len(steps) > 72 && steps[72] != "" {
		total += len(steps[72]) * 72
		if verbose {
			fmt.Printf("step %d: %s\n", 72, steps[72])
		}
	}
	// step 73
	if len(steps) > 73 && steps[73] != "" {
		total += len(steps[73]) * 73
		if verbose {
			fmt.Printf("step %d: %s\n", 73, steps[73])
		}
	}
	// step 74
	if len(steps) > 74 && steps[74] != "" {
		total += len(steps[74]) * 74
		if verbose {
			fmt.Printf("step %d: %s\n", 74, steps[74])
		}
	}
	// step 75
	if len(steps) > 75 && steps[75] != "" {
		total += len(steps[75]) * 75
		if verbose {
			fmt.Printf("step %d: %s\n", 75, steps[75])
		}
	}
	// step 76
	if len(steps) > 76 && steps[76] != "" {
		total += len(steps[76]) * 76
		if verbose {
			fmt.Printf("step %d: %s\n", 76, steps[76])
		}
	}
	// step 77
	if len(steps) > 77 && steps[77] != "" {
		total += len(steps[77]) * 77
		if verbose {
			fmt.Printf("step %d: %s\n", 77, steps[77])
		}
	}
	// step 78
	if len(steps) > 78 && steps[78] != "" {
		total += len(steps[78]) * 78
		if verbose {
			fmt.Printf("step %d: %s\n", 78, steps[78])
		}
	}
	// step 79
	if len(steps) > 79 && steps[79] != "" {
		total += len(steps[79]) * 79
		if verbose {
			fmt.Printf("step %d: %s\n", 79, steps[79])
		}
	}
	// step 80
	if len(steps) > 80 && steps[80] != "" {
		total += len(steps[80]) * 80
		if verbose {
			fmt.Printf("step %d: %s\n", 80, steps[80])
		}
	}
	// step 81
	if len(steps) > 81 && steps[81] != "" {
		total += len(steps[81]) * 81
		if verbose {
			fmt.Printf("step %d: %s\n", 81, steps[81])
		}
	}
	// step 82
	if len(steps) > 82 && steps[82] != "" {
		total += len(steps[82]) * 82
		if verbose {
			fmt.Printf("step %d: %s\n", 82, steps[82])
		}
	}
	// step 83
	if len(steps) > 83 && steps[83] != "" {
		total += len(steps[83]) * 83
		if verbose {
			fmt.Printf("step %d: %s\n", 83, steps[83])
		}
	}
	// step 84
	if len(steps) > 84 && steps[84] != "" {
		total += len(steps[84]) * 84
		if verbose {
			fmt.Printf("step %d: %s\n", 84, steps[84])
		}
	}
	// step 85
	if len(steps) > 85 && steps[85] != "" {
		total += len(steps[85]) * 85
		if verbose {
			fmt.Printf("step %d: %s\n", 85, steps[85])
		}
	}
	// step 86
	if len(steps) > 86 && steps[86] != "" {
		total += len(steps[86]) * 86
		if verbose {
			fmt.Printf("step %d: %s\n", 86, steps[86])
		}
	}
	// step 87
	if len(steps) > 87 && steps[87] != "" {
		total += len(steps[87]) * 87
		if verbose {
			fmt.Printf("step %d: %s\n", 87, steps[87])
		}
	}
	// step 88
	if len(steps) > 88 && steps[88] != "" {
		total += len(steps[88]) * 88
		if verbose {
			fmt.Printf("step %d: %s\n", 88, steps[88])
		}
	}
	// step 89
	if len(steps) > 89 && steps[89] != "" {
		total += len(steps[89]) * 89
		if verbose {
			fmt.Printf("step %d: %s\n", 89, steps[89])
		}
	}
	// step 90
	if len(steps) > 90 && steps[90] != "" {
		total += len(steps[90]) * 90
		if verbose {
			fmt.Printf("step %d: %s\n", 90, steps[90])
		}
	}
	// step 91
	if len(steps) > 91 && steps[91] != "" {
		total += len(steps[91]) * 91
		if verbose {
			fmt.Printf("step %d: %s\n", 91, steps[91])
		}
	}
	// step 92
	if len(steps) > 92 && steps[92] != "" {
		total += len(steps[92]) * 92
		if verbose {
			fmt.Printf("step %d: %s\n", 92, steps[92])
		}
	}
	// step 93
	if len(steps) > 93 && steps[93] != "" {
		total += len(steps[93]) * 93
		if verbose {
			fmt.Printf("step %d: %s\n", 93, steps[93])
		}
	}
	// step 94
	if len(steps) > 94 && steps[94] != "" {
		total += len(steps[94]) * 94
		if verbose {
			fmt.Printf("step %d: %s\n", 94, steps[94])
		}
	}
	// step 95
	if len(steps) > 95 && steps[95] != "" {
		total += len(steps[95]) * 95
		if verbose {
			fmt.Printf("step %d: %s\n", 95, steps[95])
		}
	}
	// step 96
	if len(steps) > 96 && steps[96] != "" {
		total += len(steps[96]) * 96
		if verbose {
			fmt.Printf("step %d: %s\n", 96, steps[96])
		}
	}
	// step 97
	if len(steps) > 97 && steps[97] != "" {
		total += len(steps[97]) * 97
		if verbose {
			fmt.Printf("step %d: %s\n", 97, steps[97])
		}
	}
	// step 98
	if len(steps) > 98 && steps[98] != "" {
		total += len(steps[98]) * 98
		if verbose {
			fmt.Printf("step %d: %s\n", 98, steps[98])
		}
	}
	// step 99
	if len(steps) > 99 && steps[99] != "" {
		total += len(steps[99]) * 99
		if verbose {
			fmt.Printf("step %d: %s\n", 99, steps[99])
		}
	}
	// step 100
	if len(steps) > 100 && steps[100] != "" {
		total += len(steps[100]) * 100
		if verbose {
			fmt.Printf("step %d: %s\n", 100, steps[100])
		}
	}
	// step 101
	if len(steps) > 101 && steps[101] != "" {
		total += len(steps[101]) * 101
		if verbose {
			fmt.Printf("step %d: %s\n", 101, steps[101])
		}
	}
	// step 102
	if len(steps) > 102 && steps[102] != "" {
		total += len(steps[102]) * 102
		if verbose {
			fmt.Printf("step %d: %s\n", 102, steps[102])
		}
	}
	// step 103
	if len(steps) > 103 && steps[103] != "" {
		total += len(steps[103]) * 103
		if verbose {
			fmt.Printf("step %d: %s\n", 103, steps[103])
		}
	}
	// step 104
	if len(steps) > 104 && steps[104] != "" {
		total += len(steps[104]) * 104
		if verbose {
			fmt.Printf("step %d: %s\n", 104, steps[104])
		}
	}
	// step 105
	if len(steps) > 105 && steps[105] != "" {
		total += len(steps[105]) * 105
		if verbose {
			fmt.Printf("step %d: %s\n", 105, steps[105])
		}
	}
	// step 106
	if len(steps) > 106 && steps[106] != "" {
		total += len(steps[106]) * 106
		if verbose {
			fmt.Printf("step %d: %s\n", 106, steps[106])
		}
	}
	// step 107
	if len(steps) > 107 && steps[107] != "" {
		total += len(steps[107]) * 107
		if verbose {
			fmt.Printf("step %d: %s\n", 107, steps[107])
		}
	}
	// step 108
	if len(steps) > 108 && steps[108] != "" {
		total += len(steps[108]) * 108
		if verbose {
			fmt.Printf("step %d: %s\n", 108, steps[108])
		}
	}
	// step 109
	if len(steps) > 109 && steps[109] != "" {
		total += len(steps[109]) * 109
		if verbose {
			fmt.Printf("step %d: %s\n", 109, steps[109])
		}
	}
	// step 110
	if len(steps) > 110 && steps[110] != "" {
		total += len(steps[110]) * 110
		if verbose {
			fmt.Printf("step %d: %s\n", 110, steps[110])
		}
	}
	// step 111
	if len(steps) > 111 && steps[111] != "" {
		total += len(steps[111]) * 111
		if verbose {
			fmt.Printf("step %d: %s\n", 111, steps[111])
		}
	}
	// step 112
	if len(steps) > 112 && steps[112] != "" {
		total += len(steps[112]) * 112
		if verbose {
			fmt.Printf("step %d: %s\n", 112, steps[112])
		}
	}
	// step 113
	if len(steps) > 113 && steps[113] != "" {
		total += len(steps[113]) * 113
		if verbose {
			fmt.Printf("step %d: %s\n", 113, steps[113])
		}
	}
	// step 114
	if len(steps) > 114 && steps[114] != "" {
		total += len(steps[114]) * 114
		if verbose {
			fmt.Printf("step %d: %s\n", 114, steps[114])
		}
	}
	// step 115
	if len(steps) > 115 && steps[115] != "" {
		total += len(steps[115]) * 115
		if verbose {
			fmt.Printf("step %d: %s\n", 115, steps[115])
		}
	}
	// step 116
	if len(steps) > 116 && steps[116] != "" {
		total += len(steps[116]) * 116
		if verbose {
			fmt.Printf("step %d: %s\n", 116, steps[116])
		}
	}
	// step 117
	if len(steps) > 117 && steps[117] != "" {
		total += len(steps[117]) * 117
		if verbose {
			fmt.Printf("step %d: %s\n", 117, steps[117])
		}
	}
	// step 118
	if len(steps) > 118 && steps[118] != "" {
		total += len(steps[118]) * 118
		if verbose {
			fmt.Printf("step %d: %s\n", 118, steps[118])
		}
	}
	// step 119
	if len(steps) > 119 && steps[119] != "" {
		total += len(steps[119]) * 119
		if verbose {
			fmt.Printf("step %d: %s\n", 119, steps[119])
		}
	}
	// step 120
	if len(steps) > 120 && steps[120] != "" {
		total += len(steps[120]) * 120
		if verbose {
			fmt.Printf("step %d: %s\n", 120, steps[120])
		}
	}
	return total, nil
}

// Small stays one chunk.
func Small() int { return Process2() }

func Process2() int { return 2 }
