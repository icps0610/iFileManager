package fsops

// DiffDriveMask 比較前後兩次的磁碟機遮罩，回傳新增與移除的磁碟機根目錄（例如 E:\）。
func DiffDriveMask(before, after uint32) (added, removed []string) {
	for i := 0; i < 26; i++ {
		bit := uint32(1) << uint(i)
		root := string(rune('A'+i)) + `:\`
		switch {
		case after&bit != 0 && before&bit == 0:
			added = append(added, root)
		case after&bit == 0 && before&bit != 0:
			removed = append(removed, root)
		}
	}
	return added, removed
}
